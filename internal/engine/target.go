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

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/internal/merge"
	syncpkg "github.com/agent-sync/agent-sync/internal/sync"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// genTimestampFormat is a fixed-width, lexically-sortable instant used
// for staging generation directory names.
const genTimestampFormat = "20060102T150405Z"

// emitOutcome is the adapter-run result shared by the sync and dry-run
// paths: the decoded ops plus the declared owned-subdir prefixes and any
// warning notes.
type emitOutcome struct {
	ops            []contract.Op
	ownedPrefixes  []string // OutputModeOwnedSubdir declared output paths
	sharedPrefixes []string // OutputModeSharedSubdir declared output paths
	warnings       []string
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

	// The runtime's declared-outputs and capability-lied gates run against
	// OpsPerformed (the {kind, path} summary), but the engine performs
	// writes from Ops (the full envelopes). Require the two to agree
	// 1:1 in order so a legacy adapter that returns a summary with no
	// content (which would otherwise be read as "delete everything") and a
	// buggy/malicious adapter whose Ops diverge from the gated summary are
	// both rejected rather than silently trusted.
	if len(emitResult.Ops) != len(emitResult.OpsPerformed) {
		return emitOutcome{}, fmt.Errorf("%w: %q returned %d op envelopes but %d op summaries",
			ErrEmitOpsMismatch, target, len(emitResult.Ops), len(emitResult.OpsPerformed))
	}

	out := emitOutcome{
		ownedPrefixes:  ownedSubdirs(initResult.DeclaredOutputs),
		sharedPrefixes: sharedSubdirs(initResult.DeclaredOutputs),
	}
	for i, raw := range emitResult.Ops {
		op, derr := contract.DecodeOp(raw)
		if derr != nil {
			return emitOutcome{}, fmt.Errorf("engine: decode op[%d] for %q: %w", i, target, derr)
		}
		rec := emitResult.OpsPerformed[i]
		if string(op.OpKind()) != string(rec.Op) || op.OpPath() != rec.Path {
			return emitOutcome{}, fmt.Errorf("%w: %q op[%d] envelope {%s %q} != summary {%s %q}",
				ErrEmitOpsMismatch, target, i, op.OpKind(), op.OpPath(), rec.Op, rec.Path)
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

// sharedSubdirs returns the declared-output paths whose mode is shared-subdir.
// These are directories agent-sync shares with the user and other tools; the
// engine never swaps the shared parent itself, only the agent-sync-owned leaf
// entries within it (see effectiveOwnedPrefixes).
func sharedSubdirs(outputs []contract.DeclaredOutput) []string {
	var shared []string
	for _, o := range outputs {
		if o.Mode == contract.OutputModeSharedSubdir {
			shared = append(shared, o.Path)
		}
	}
	// Longest-first so leafUnder's linear scan picks the most-specific prefix
	// when shared prefixes nest — same invariant ownedSubdirs relies on.
	sort.Slice(shared, func(i, j int) bool { return len(shared[i]) > len(shared[j]) })
	return shared
}

// leafUnder returns the agent-sync-managed leaf directory of p within one of
// the shared prefixes — i.e. "<shared>/<firstSegment>" — or "" when p is not
// under any shared prefix. The leaf (not the shared parent) is the swap unit
// for shared-subdir outputs, so foreign sibling leaves are never touched.
func leafUnder(shared []string, p string) string {
	for _, sp := range shared {
		if !strings.HasPrefix(p, sp+"/") {
			continue
		}
		rest := strings.TrimPrefix(p, sp+"/")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i] // first path segment below the shared parent
		}
		// A managed leaf must be a real child-dir name. Reject empty / dot /
		// parent segments — defense-in-depth; the runtime declared-outputs gate
		// already path.Cleans and rejects traversal before ops reach here.
		if rest == "" || rest == "." || rest == ".." {
			return ""
		}
		return sp + "/" + rest
	}
	return ""
}

// effectiveOwnedPrefixes is the set of prefixes the engine treats as owned for
// stage+swap, drift, and orphan purposes: every owned-subdir prefix, plus — for
// each shared-subdir — only the agent-sync-managed leaf directories within it,
// derived from this run's emitted ops and the prior ledger. The shared parent
// is deliberately absent, so it is never swapped wholesale and foreign sibling
// leaves (never emitted, never in the ledger) are invisible to the engine.
// Sorted longest-first so the most specific prefix wins when paths nest.
func effectiveOwnedPrefixes(owned, shared []string, ops []contract.Op, ledgerEntries []ledger.Entry) []string {
	effective := append([]string(nil), owned...)
	if len(shared) > 0 {
		leaves := map[string]struct{}{}
		add := func(p string) {
			if leaf := leafUnder(shared, p); leaf != "" {
				leaves[leaf] = struct{}{}
			}
		}
		for _, op := range ops {
			add(op.OpPath())
		}
		for _, e := range ledgerEntries {
			add(e.Path)
		}
		for leaf := range leaves {
			effective = append(effective, leaf)
		}
	}
	sort.Slice(effective, func(i, j int) bool { return len(effective[i]) > len(effective[j]) })
	return effective
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

	// effective is the owned set the rest of the pipeline operates on: every
	// owned-subdir prefix plus, for each shared-subdir, only the agent-sync
	// leaf dirs (from this run's ops + the prior ledger). The shared parent is
	// never in this set, so it is never drift-scanned, swapped, or orphaned —
	// foreign sibling content under a shared tree is invisible to the engine.
	effective := effectiveOwnedPrefixes(out.ownedPrefixes, out.sharedPrefixes, out.ops, oldLedger.Entries)

	// Drift gate per owned subdir (per managed leaf for shared subdirs),
	// unless adopting.
	adoptedAny := false
	for _, pre := range effective {
		if scanErr := syncpkg.ScanDrift(root, pre, oldLedger); scanErr != nil {
			if adopting(req.Options.AdoptPrefixes, pre) {
				if _, aerr := syncpkg.AdoptEntries(root, pre, now); aerr != nil {
					return statusResult{}, fmt.Errorf("engine: adopt %s: %w", pre, aerr)
				}
				adoptedAny = true
				continue
			}
			return statusResult{}, fmt.Errorf("%w: %s: %w", ErrDrift, pre, scanErr)
		}
	}
	// AdoptEntries wrote new entries to the on-disk ledger; reload so
	// change-counting, status, and orphan detection see the adopted set
	// (an adopted-then-undesired file must still be detected as an orphan).
	if adoptedAny {
		oldLedger, err = loadLedger(root, target)
		if err != nil {
			return statusResult{}, err
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
	// fileOutputs are write ops whose path is exactly an owned-output path
	// (e.g. the .agent-sync-managed sidecar): a single managed file, not a
	// directory tree. They are written atomically via StagedWrite, never
	// staged-and-swapped as a directory (which would nest the file inside a
	// like-named dir).
	var fileOutputs []contract.OpWriteFile
	// fileType marks owned-output paths that name a single file rather than
	// a subdir tree, so the directory swap loop skips them.
	fileType := map[string]bool{}
	newEntries := map[string]ledger.Entry{}

	for _, op := range out.ops {
		switch o := op.(type) {
		case contract.OpWriteFile:
			pre := ownerOf(effective, o.Path)
			if pre == "" {
				return statusResult{}, fmt.Errorf("engine: write_file %q outside any owned subdir", o.Path)
			}
			h := sha256Hex(o.Content)
			bumpChanged(o.Path, h)
			newEntries[o.Path] = ledger.Entry{Path: o.Path, SHA256: h, Size: int64(len(o.Content)), EmittedAt: now}
			if o.Path == pre {
				// Single-file owned output.
				fileOutputs = append(fileOutputs, o)
				fileType[pre] = true
				continue
			}
			w := bySubdir[pre]
			if w == nil {
				w = &subdirWork{}
				bySubdir[pre] = w
			}
			w.writes = append(w.writes, o)
		case contract.OpMkdir:
			pre := ownerOf(effective, o.Path)
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
	// A prior single-file output (ledger entry whose path equals an owned
	// output exactly) is also file-type, so its removal is handled by
	// orphan deletion rather than a directory swap.
	oldHadUnder := map[string]bool{}
	for _, e := range oldLedger.Entries {
		if pre := ownerOf(effective, e.Path); pre != "" {
			oldHadUnder[pre] = true
			if e.Path == pre {
				fileType[pre] = true
			}
		}
	}

	// Orphan set = previously-managed paths under an owned prefix no longer
	// desired. Compute it and enforce the --expect-deletions guard BEFORE
	// any mutation, so a mismatched count aborts with every prefix
	// byte-intact rather than after files have already been swapped away.
	// Tool-owned paths are excluded (ownerOf == "") so a shared file is
	// never counted or deleted as an orphan.
	var deletable []string
	for _, e := range oldLedger.Entries {
		if ownerOf(effective, e.Path) == "" {
			continue
		}
		if _, wanted := newEntries[e.Path]; !wanted {
			deletable = append(deletable, e.Path)
		}
	}
	sort.Strings(deletable)
	expectedDeletions := -1 // -1 = unspecified, always passes
	if req.Options.ExpectDeletions != nil {
		expectedDeletions = *req.Options.ExpectDeletions
	}
	if cerr := syncpkg.CheckExpectedDeletions(expectedDeletions, len(deletable)); cerr != nil {
		return statusResult{}, cerr
	}

	// Single-file owned outputs: write atomically in place. StagedWrite is
	// itself temp-write + fsync + rename, so no directory swap is needed.
	for _, fo := range fileOutputs {
		if mkErr := root.Inner().MkdirAll(path.Dir(fo.Path), 0o755); mkErr != nil {
			return statusResult{}, fmt.Errorf("engine: mkdir parent of %s: %w", fo.Path, mkErr)
		}
		mode := fo.Mode
		if mode == 0 {
			mode = 0o644
		}
		if werr := root.StagedWrite(fo.Path, fo.Content, fsModeOf(mode)); werr != nil {
			return statusResult{}, fmt.Errorf("engine: write file output %s: %w", fo.Path, werr)
		}
	}

	// Stage + swap each relevant owned subdir (directory-tree outputs).
	// Order is sorted for determinism.
	subdirs := append([]string(nil), effective...)
	sort.Strings(subdirs)
	recovered := map[string]bool{}
	for _, pre := range subdirs {
		if fileType[pre] {
			continue
		}
		w := bySubdir[pre]
		if w == nil && !oldHadUnder[pre] {
			continue
		}
		parentRel := path.Dir(pre)
		// Recovery is keyed on the staging parent, which is nested under
		// each prefix's parent — a top-level Recover(".") never sees it.
		// Drive any half-finished swap for this parent to a clean state
		// before re-staging (AGENTS invariant #6).
		if !recovered[parentRel] {
			if _, rerr := syncpkg.Recover(root, parentRel); rerr != nil {
				return statusResult{}, fmt.Errorf("engine: recover %s: %w", parentRel, rerr)
			}
			recovered[parentRel] = true
		}
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
				dst := path.Join(stagingLeafRel, rel)
				if !within(stagingLeafRel, dst) {
					return statusResult{}, fmt.Errorf("engine: mkdir %q escapes owned prefix %q", m.Path, pre)
				}
				if mkErr := root.Inner().MkdirAll(dst, 0o755); mkErr != nil {
					return statusResult{}, fmt.Errorf("engine: stage mkdir %s: %w", rel, mkErr)
				}
			}
			for _, wf := range w.writes {
				rel := strings.TrimPrefix(wf.Path, pre+"/")
				dst := path.Join(stagingLeafRel, rel)
				if !within(stagingLeafRel, dst) {
					return statusResult{}, fmt.Errorf("engine: write_file %q escapes owned prefix %q", wf.Path, pre)
				}
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
		holder := "engine:" + target
		for _, o := range toolOwned {
			entry := merge.MergeEntry{Kind: adapterkit.ToolOwnedKind(o.Kind), Locator: o.Locator, Content: o.Content}
			sliceHash, _, merr := merge.ApplyToFile(ctx, root, reg, o.Path, entry, holder)
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

	// Orphan deletion uses the deletable set computed and guarded before
	// mutation (above), restricted to owned-subdir paths so a shared
	// tool-owned file is never removed. Runs only after the new ledger is
	// durable, so a crash mid-delete is recoverable (AGENTS invariant #7).
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

// within reports whether target (a slash path) is lexically contained
// within baseRel after cleaning — a defense against adapter op paths
// containing ".." that path.Join would resolve outside the staged
// prefix. The fsroot layer blocks root escape; this blocks in-root
// prefix escape into another adapter's territory or user files.
func within(baseRel, target string) bool {
	b := path.Clean(baseRel)
	t := path.Clean(target)
	return t == b || strings.HasPrefix(t, b+"/")
}
