package engine

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/aienvs/aienvs/internal/report"
	syncpkg "github.com/aienvs/aienvs/internal/sync"
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

	// Recovery is idempotent and global; drive any half-finished swap to a
	// clean state before touching anything.
	if _, err := syncpkg.Recover(req.Root, "."); err != nil {
		return report.Summary{}, err
	}

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
