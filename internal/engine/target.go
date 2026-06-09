package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/adapter/contract"
	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/ledger"
	"github.com/aienvs/aienvs/internal/locks"
	"github.com/aienvs/aienvs/internal/merge"
	syncpkg "github.com/aienvs/aienvs/internal/sync"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

// genTimestampFormat is a fixed-width, lexically-sortable instant used
// for staging generation directory names.
const genTimestampFormat = "20060102T150405Z"

// emitOutcome is the adapter-run result shared by the sync and dry-run
// paths: the decoded ops plus the declared owned-subdir prefixes and any
// warning notes.
type emitOutcome struct {
	ops           []contract.Op
	ownedPrefixes []string // OutputModeOwnedSubdir declared output paths
	warnings      []string
}

// runAdapter drives one adapter's full session lifecycle and returns the
// decoded ops (with content, via the U0 channel). It performs no
// filesystem writes — both Sync and Plan call it.
func runAdapter(ctx context.Context, req Request, target string) (emitOutcome, error) {
	a, ok := req.Registry.Get(target)
	if !ok {
		return emitOutcome{}, fmt.Errorf("%w: %q", ErrAdapterNotFound, target)
	}

	session, err := a.NewSession(ctx, adapter.SessionOptions{
		WorkspaceRoot: req.WorkspacePath,
		IRVersion:     "v1",
	})
	if err != nil {
		return emitOutcome{}, fmt.Errorf("engine: new session %q: %w", target, err)
	}
	// Shutdown always runs, detached from ctx cancellation, mirroring the
	// conformance harness.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = session.Shutdown(shutdownCtx)
	}()

	initResult, err := session.Initialize(ctx)
	if err != nil {
		return emitOutcome{}, fmt.Errorf("engine: initialize %q: %w", target, err)
	}
	if err := session.Initialized(ctx); err != nil {
		return emitOutcome{}, fmt.Errorf("engine: initialized %q: %w", target, err)
	}

	irPayload, err := MarshalIR(req.Nodes, req.Skills)
	if err != nil {
		return emitOutcome{}, err
	}

	emitResult, err := session.Emit(ctx, target, irPayload)
	if err != nil {
		return emitOutcome{}, fmt.Errorf("engine: emit %q: %w", target, err)
	}

	out := emitOutcome{ownedPrefixes: ownedSubdirs(initResult.DeclaredOutputs)}
	for i, raw := range emitResult.Ops {
		op, derr := contract.DecodeOp(raw)
		if derr != nil {
			return emitOutcome{}, fmt.Errorf("engine: decode op[%d] for %q: %w", i, target, derr)
		}
		if w, ok := op.(contract.OpWarning); ok {
			out.warnings = append(out.warnings, fmt.Sprintf("%s: %s", w.ConceptID, w.Note))
			continue
		}
		out.ops = append(out.ops, op)
	}
	return out, nil
}

// ownedSubdirs returns the declared-output paths whose mode is
// owned-subdir, sorted longest-first so the most specific prefix wins
// when paths nest.
func ownedSubdirs(outputs []contract.DeclaredOutput) []string {
	var owned []string
	for _, o := range outputs {
		if o.Mode == contract.OutputModeOwnedSubdir {
			owned = append(owned, o.Path)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return len(owned[i]) > len(owned[j]) })
	return owned
}

// ownerOf returns the owned-subdir prefix that contains p, or "" if none.
func ownerOf(owned []string, p string) string {
	for _, pre := range owned {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return pre
		}
	}
	return ""
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// applyTarget performs the full write pipeline for one target: drift
// check, lock, adapter run, per-owned-subdir stage+swap, tool-owned
// merge, durable ledger write, then orphan deletion. Returns the
// per-target report. Errors are returned for the engine to classify by
// mode.
func applyTarget(ctx context.Context, req Request, target string, now time.Time) (status statusResult, err error) {
	root := req.Root
	oldLedger, err := loadLedger(root, target)
	if err != nil {
		return statusResult{}, err
	}

	out, err := runAdapter(ctx, req, target)
	if err != nil {
		return statusResult{}, err
	}

	// Drift gate per owned subdir, unless adopting.
	for _, pre := range out.ownedPrefixes {
		if scanErr := syncpkg.ScanDrift(root, pre, oldLedger); scanErr != nil {
			if adopting(req.Options.AdoptPrefixes, pre) {
				if _, aerr := syncpkg.AdoptEntries(root, pre, now); aerr != nil {
					return statusResult{}, fmt.Errorf("engine: adopt %s: %w", pre, aerr)
				}
				continue
			}
			return statusResult{}, fmt.Errorf("%w: %s: %w", ErrDrift, pre, scanErr)
		}
	}

	// Whole-sync lock for this target.
	lock, err := locks.NewTargetLock(root, target)
	if err != nil {
		return statusResult{}, fmt.Errorf("engine: lock %q: %w", target, err)
	}
	release, err := lock.Acquire(ctx, locks.AcquireOpts{})
	if err != nil {
		if errors.Is(err, locks.ErrTargetLocked) {
			return statusResult{status: statusBlocked}, nil
		}
		return statusResult{}, fmt.Errorf("engine: acquire lock %q: %w", target, err)
	}
	defer func() { _ = release() }()

	gen := syncpkg.Meta{Timestamp: now.UTC().Format(genTimestampFormat), SHA: commitOrPlaceholder(req.Commit)}

	oldByPath := map[string]ledger.Entry{}
	for _, e := range oldLedger.Entries {
		oldByPath[e.Path] = e
	}
	// changedCount tracks net content changes (create + update) so an
	// idempotent re-sync reports "unchanged" even though the swap rewrites
	// the subdir wholesale.
	changedCount := 0
	bumpChanged := func(p, hash string) {
		if prev, ok := oldByPath[p]; !ok || prev.SHA256 != hash {
			changedCount++
		}
	}

	// Group write/mkdir ops by owned subdir; collect tool-owned ops.
	type subdirWork struct {
		writes []contract.OpWriteFile
		mkdirs []contract.OpMkdir
	}
	bySubdir := map[string]*subdirWork{}
	var toolOwned []contract.OpWriteToolOwned
	newEntries := map[string]ledger.Entry{}

	for _, op := range out.ops {
		switch o := op.(type) {
		case contract.OpWriteFile:
			pre := ownerOf(out.ownedPrefixes, o.Path)
			if pre == "" {
				return statusResult{}, fmt.Errorf("engine: write_file %q outside any owned subdir", o.Path)
			}
			w := bySubdir[pre]
			if w == nil {
				w = &subdirWork{}
				bySubdir[pre] = w
			}
			w.writes = append(w.writes, o)
			h := sha256Hex(o.Content)
			bumpChanged(o.Path, h)
			newEntries[o.Path] = ledger.Entry{Path: o.Path, SHA256: h, Size: int64(len(o.Content)), EmittedAt: now}
		case contract.OpMkdir:
			pre := ownerOf(out.ownedPrefixes, o.Path)
			if pre == "" {
				return statusResult{}, fmt.Errorf("engine: mkdir %q outside any owned subdir", o.Path)
			}
			w := bySubdir[pre]
			if w == nil {
				w = &subdirWork{}
				bySubdir[pre] = w
			}
			w.mkdirs = append(w.mkdirs, o)
		case contract.OpWriteToolOwned:
			toolOwned = append(toolOwned, o)
		case contract.OpDelete:
			// Explicit deletes are handled by the orphan pass via the ledger.
		}
	}

	counts := statusCounts{}

	// An owned subdir is swapped when it has ops this run OR previously
	// held managed entries (so removed files vanish atomically). Subdirs
	// that are empty in both states are skipped to avoid spurious dirs.
	oldHadUnder := map[string]bool{}
	for _, e := range oldLedger.Entries {
		if pre := ownerOf(out.ownedPrefixes, e.Path); pre != "" {
			oldHadUnder[pre] = true
		}
	}

	// Stage + swap each relevant owned subdir. Order is sorted for
	// determinism.
	subdirs := append([]string(nil), out.ownedPrefixes...)
	sort.Strings(subdirs)
	for _, pre := range subdirs {
		w := bySubdir[pre]
		if w == nil && !oldHadUnder[pre] {
			continue
		}
		parentRel := path.Dir(pre)
		leaf := path.Base(pre)
		stagingLeafRel, serr := syncpkg.Stage(root, parentRel, leaf, gen)
		if serr != nil {
			return statusResult{}, serr
		}
		if w != nil {
			for _, m := range w.mkdirs {
				rel := strings.TrimPrefix(m.Path, pre)
				rel = strings.TrimPrefix(rel, "/")
				if rel == "" {
					continue
				}
				if mkErr := root.Inner().MkdirAll(path.Join(stagingLeafRel, rel), 0o755); mkErr != nil {
					return statusResult{}, fmt.Errorf("engine: stage mkdir %s: %w", rel, mkErr)
				}
			}
			for _, wf := range w.writes {
				rel := strings.TrimPrefix(wf.Path, pre+"/")
				dst := path.Join(stagingLeafRel, rel)
				if mkErr := root.Inner().MkdirAll(path.Dir(dst), 0o755); mkErr != nil {
					return statusResult{}, fmt.Errorf("engine: stage parent %s: %w", dst, mkErr)
				}
				mode := wf.Mode
				if mode == 0 {
					mode = 0o644
				}
				if werr := root.StagedWrite(dst, wf.Content, fsModeOf(mode)); werr != nil {
					return statusResult{}, fmt.Errorf("engine: stage write %s: %w", dst, werr)
				}
			}
		}
		sentinel := syncpkg.Sentinel{
			Workspace:      req.WorkspacePath,
			Target:         target,
			SHA:            gen.SHA,
			StartedAt:      now.UTC().Format(time.RFC3339),
			PrefixRel:      pre,
			StagingLeafRel: stagingLeafRel,
		}
		if swErr := syncpkg.Swap(root, sentinel); swErr != nil {
			return statusResult{}, fmt.Errorf("engine: swap %s: %w", pre, swErr)
		}
	}

	// Tool-owned merges, in place.
	if len(toolOwned) > 0 {
		reg, regErr := locks.NewFileLockRegistry(root)
		if regErr != nil {
			return statusResult{}, fmt.Errorf("engine: file lock registry: %w", regErr)
		}
		for _, o := range toolOwned {
			entry := merge.MergeEntry{Kind: adapterkit.ToolOwnedKind(o.Kind), Locator: o.Locator, Content: o.Content}
			absLockPath := path.Join(req.WorkspacePath, o.Path)
			sliceHash, _, merr := merge.ApplyToFile(ctx, root, reg, o.Path, entry, absLockPath)
			if merr != nil {
				return statusResult{}, fmt.Errorf("engine: merge %s: %w", o.Path, merr)
			}
			bumpChanged(o.Path, sliceHash)
			newEntries[o.Path] = ledger.Entry{Path: o.Path, SHA256: sliceHash, Size: int64(len(o.Content)), EmittedAt: now}
		}
	}

	// Build and durably write the new ledger BEFORE deleting orphans.
	next := ledger.Ledger{SchemaVersion: ledger.SchemaVersionCurrent, Target: target}
	for _, e := range newEntries {
		next.Entries = append(next.Entries, e)
	}
	if werr := ledger.Write(root, next); werr != nil {
		return statusResult{}, fmt.Errorf("engine: write ledger %q: %w", target, werr)
	}

	// Orphan deletion: previous_ledger − current_desired, restricted to
	// owned-subdir paths so a shared tool-owned file is never removed
	// (its slices are upserted, not file-deleted, in v1).
	orphans := syncpkg.Orphans(oldLedger, next)
	var deletable []string
	for _, p := range orphans {
		if ownerOf(out.ownedPrefixes, p) != "" {
			deletable = append(deletable, p)
		}
	}
	if cerr := syncpkg.CheckExpectedDeletions(req.Options.ExpectDeletions, len(deletable)); cerr != nil {
		return statusResult{}, cerr
	}
	deleted, derr := syncpkg.DeleteOrphans(root, deletable)
	if derr != nil {
		return statusResult{}, fmt.Errorf("engine: delete orphans %q: %w", target, derr)
	}
	counts.written = changedCount
	counts.deleted = len(deleted)
	counts.warnings = len(out.warnings)

	paths := make([]string, 0, len(newEntries))
	for p := range newEntries {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	st := statusOK
	if counts.written == 0 && counts.deleted == 0 {
		st = statusUnchanged
	}
	return statusResult{status: st, counts: counts, paths: paths, warnings: out.warnings}, nil
}

func loadLedger(root *fsroot.Root, target string) (ledger.Ledger, error) {
	led, err := ledger.Load(root, target)
	if err != nil {
		if errors.Is(err, ledger.ErrLedgerNotFound) {
			return ledger.Ledger{SchemaVersion: ledger.SchemaVersionCurrent, Target: target}, nil
		}
		return ledger.Ledger{}, fmt.Errorf("engine: load ledger %q: %w", target, err)
	}
	return led, nil
}

func adopting(prefixes []string, pre string) bool {
	for _, p := range prefixes {
		if p == pre {
			return true
		}
	}
	return false
}

func commitOrPlaceholder(c string) string {
	if c == "" {
		return "0000000000000000000000000000000000000000"
	}
	return c
}
