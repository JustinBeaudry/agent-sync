package engine

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/internal/report"
	syncpkg "github.com/agent-sync/agent-sync/internal/sync"
)

// Sentinel errors callers branch on with errors.Is.
var (
	// ErrAdapterNotFound is returned when a manifest target has no
	// matching entry in the discovered registry.
	ErrAdapterNotFound = errors.New("engine: adapter not found for target")

	// ErrDrift is returned when a reserved prefix holds unmanaged
	// content and the prefix was not listed in Options.AdoptPrefixes.
	ErrDrift = errors.New("engine: reserved prefix has drift; adopt or resolve")

	// ErrEmitOpsMismatch is returned when an adapter's EmitResult.Ops
	// (full op envelopes) does not agree 1:1 with EmitResult.OpsPerformed
	// (the gated {kind, path} summary). The engine refuses to write a set
	// the runtime gates never vetted.
	ErrEmitOpsMismatch = errors.New("engine: emit ops do not match the op summary")
)

type targetStatus int

const (
	statusOK targetStatus = iota
	statusUnchanged
	statusBlocked
)

type statusCounts struct {
	written  int
	deleted  int
	warnings int
}

type statusResult struct {
	status   targetStatus
	counts   statusCounts
	paths    []string
	warnings []string
}

func fsModeOf(mode uint32) fs.FileMode { return fs.FileMode(mode) }

// warnOrphanLedgers surfaces (without deleting) any on-disk per-target ledger
// whose target is no longer in the manifest. Such a target's files are stranded
// — nothing revisits its ledger (ADV-1 dropped-target leak) — until
// `agent-sync unmanage <target>` reclaims them. Read-only and best-effort: a
// missing state dir or unreadable ledger is silently skipped (never fails the
// sync). Reclaiming is deliberately NOT done here: deleting a dropped target's
// files as a side effect of an unrelated sync would be surprising data loss;
// that is `unmanage`'s job.
func warnOrphanLedgers(req Request, log *slog.Logger) {
	configured := make(map[string]bool, len(req.Targets))
	for _, t := range req.Targets {
		configured[t] = true
	}
	d, err := req.Root.Inner().Open(".agent-sync/state")
	if err != nil {
		return // no state dir yet
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		target := strings.TrimSuffix(name, ".json")
		if configured[target] {
			continue
		}
		// Positively identify a real per-target ledger before warning: the state
		// dir also holds non-ledger files (e.g. capability-report.json), which
		// ledger.Load rejects via strict decode. A current-schema ledger loads
		// cleanly; an old-schema orphan returns a populated, target-checked
		// ledger alongside ErrLedgerSchemaTooOld (still a genuine orphan worth
		// its count). Anything else (corrupt, foreign, schema-too-new) is skipped
		// — never a spurious "unmanage capability-report" nag.
		led, lerr := ledger.Load(req.Root, target)
		switch {
		case lerr == nil, errors.Is(lerr, ledger.ErrLedgerSchemaTooOld):
			// real orphan ledger — warn below
		default:
			continue
		}
		log.Warn("orphan ledger: target not in the manifest; its emitted files are stranded until reclaimed",
			"target", target, "owned_paths", len(led.Entries),
			"hint", "run `agent-sync unmanage "+target+"` to remove them")
	}
}

// resolvedTargets applies TargetsFilter (if any) to the manifest targets,
// preserving manifest order.
func resolvedTargets(all, filter []string) []string {
	if len(filter) == 0 {
		return all
	}
	want := make(map[string]bool, len(filter))
	for _, f := range filter {
		want[f] = true
	}
	var out []string
	for _, t := range all {
		if want[t] {
			out = append(out, t)
		}
	}
	return out
}

// Sync runs the full pipeline for every target and returns the report
// summary. Recovery runs first to drive any half-finished swap to a clean
// state. In atomic mode the run aborts on the first target failure; in
// best-effort mode failures are recorded and the run continues.
func Sync(ctx context.Context, req Request) (report.Summary, error) {
	if req.Root == nil {
		return report.Summary{}, errors.New("engine: request root is nil")
	}
	if req.Registry == nil {
		return report.Summary{}, errors.New("engine: request registry is nil")
	}
	opts := req.Options
	now := opts.now()
	log := opts.logger()

	// Per-workspace run lock: serialize concurrent syncs on this workspace so
	// the cross-adapter shared-subdir read-decide-write (co-ownership, ADV-1) is
	// atomic across processes, and recovery/swaps never interleave between two
	// runs. Held for the whole run (recovery + every target), released on
	// return. Acquired before Recover so recovery is serialized too.
	// RunLockHeld lets a caller that already holds the per-workspace run lock
	// (currently only `agent-sync update`, which acquires it before re-pinning
	// the manifest so the re-pin and this sync are one atomic critical section)
	// skip re-acquiring it here. Re-acquiring would deadlock: the two RunLock
	// instances open separate flock handles on the same file, and flock(2)
	// serializes across open file descriptions even within one process. The
	// caller owns acquire + release in that mode.
	if !opts.RunLockHeld {
		runLock, err := locks.NewRunLock(req.Root)
		if err != nil {
			return report.Summary{}, err
		}
		release, err := runLock.Acquire(ctx, locks.AcquireOpts{Timeout: opts.RunLockTimeout})
		if err != nil {
			// Contention (another sync holds the workspace lock) must NOT be a hard
			// error: mirror the per-target lock's StatusBlocked outcome so a
			// post-merge hook sync yields cleanly (exit 0) and never breaks
			// `git pull` (AGENTS invariant #3). A blocked summary carries no error;
			// callers surface "blocked" (and the post-merge path writes its
			// hook-skipped marker). Any other Acquire error is a real failure.
			if errors.Is(err, locks.ErrRunLocked) {
				blocked := make([]report.TargetReport, 0)
				for _, t := range resolvedTargets(req.Targets, opts.TargetsFilter) {
					blocked = append(blocked, report.TargetReport{Target: t, Status: report.StatusBlocked})
				}
				log.Warn("another agent-sync sync is running in this workspace; skipping", "err", err)
				return report.Summarize(req.WorkspacePath, req.Commit, now.UTC().Format(time.RFC3339), opts.mode(), blocked), nil
			}
			return report.Summary{}, err
		}
		defer func() { _ = release() }()
	}

	// Recovery is idempotent and global; drive any half-finished swap to a
	// clean state before touching anything.
	if _, err := syncpkg.Recover(req.Root, "."); err != nil {
		return report.Summary{}, err
	}

	// Surface (without deleting) any on-disk ledger for a target no longer in
	// the manifest — its files are stranded until `agent-sync unmanage` reclaims
	// them (ADV-1 dropped-target leak). Read-only, best-effort.
	warnOrphanLedgers(req, log)

	targets := resolvedTargets(req.Targets, opts.TargetsFilter)
	var reports []report.TargetReport
	abort := false

	for _, target := range targets {
		if abort {
			reports = append(reports, report.TargetReport{Target: target, Status: report.StatusSkipped})
			continue
		}
		start := opts.now()
		res, err := applyTarget(ctx, req, target, now)
		dur := opts.now().Sub(start).Milliseconds()
		if err != nil {
			log.Error("target sync failed", "target", target, "err", err)
			tr := report.TargetReport{
				Target:     target,
				Status:     report.StatusFailed,
				DurationMs: dur,
				Error:      err.Error(),
			}
			reports = append(reports, tr)
			if opts.mode() == report.ModeAtomic {
				abort = true
			}
			continue
		}
		reports = append(reports, toTargetReport(target, res, dur))
	}

	// Capability report is best-effort observability; failure to persist
	// it never fails the sync.
	if err := writeCapabilityReport(ctx, req, now); err != nil {
		log.Warn("capability report not written", "err", err)
	}

	summary := report.Summarize(req.WorkspacePath, req.Commit, now.UTC().Format(time.RFC3339), opts.mode(), reports)
	return summary, nil
}

// Plan runs the pipeline up to (but not including) staging and returns
// the per-target change set without mutating the workspace. It powers
// `validate`.
func Plan(ctx context.Context, req Request) (PlanResult, error) {
	if req.Root == nil {
		return PlanResult{}, errors.New("engine: request root is nil")
	}
	if req.Registry == nil {
		return PlanResult{}, errors.New("engine: request registry is nil")
	}
	opts := req.Options
	now := opts.now()

	targets := resolvedTargets(req.Targets, opts.TargetsFilter)
	out := PlanResult{WorkspacePath: req.WorkspacePath, Commit: req.Commit}

	for _, target := range targets {
		change := planTarget(ctx, req, target, now)
		if len(change.WouldCreate) > 0 || len(change.WouldUpdate) > 0 || len(change.WouldDelete) > 0 || len(change.OutOfBand) > 0 {
			out.DriftDetected = true
		}
		out.Targets = append(out.Targets, change)
	}
	return out, nil
}

func toTargetReport(target string, res statusResult, dur int64) report.TargetReport {
	st := report.StatusOK
	switch res.status {
	case statusUnchanged:
		st = report.StatusUnchanged
	case statusBlocked:
		st = report.StatusBlocked
	}
	return report.TargetReport{
		Target:     target,
		Status:     st,
		DurationMs: dur,
		Counts: report.Counts{
			Written:   res.counts.written,
			Deleted:   res.counts.deleted,
			Warnings:  res.counts.warnings,
			Unchanged: 0,
		},
		Paths: res.paths,
	}
}
