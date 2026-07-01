package engine

import (
	"io"
	"log/slog"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/report"
)

// Options carries per-run policy. Zero values are sensible: atomic mode,
// no adopt, all targets, real clock, expect-deletions unspecified.
type Options struct {
	// Mode is atomic (abort the run on first target failure) or
	// best-effort (record the failure and continue). Empty → atomic.
	Mode report.Mode

	// AdoptPrefixes lists reserved-prefix paths whose pre-existing
	// unmanaged content should be adopted on this sync instead of
	// blocking on drift.
	AdoptPrefixes []string

	// TargetsFilter, when non-empty, restricts the sync to these target
	// names (a subset of the manifest's targets).
	TargetsFilter []string

	// ExpectDeletions is the --expect-deletions safety guard: when set, the
	// sync aborts (before any mutation) unless exactly this many orphan
	// files would be deleted. nil means unspecified (no guard). A pointer
	// rather than an int because 0 ("expect no deletions") is a meaningful
	// opt-in value distinct from "flag absent".
	ExpectDeletions *int

	// Now stamps generation timestamps and ledger/report times. Injectable
	// for deterministic tests. Defaults to time.Now.
	Now func() time.Time

	// Logger receives structured progress. Defaults to a discard logger.
	Logger *slog.Logger

	// RunLockTimeout bounds how long Sync waits for the per-workspace run lock
	// before yielding a blocked summary. 0 uses the locks package default
	// (2m, matching the per-target lock). The post-merge git-hook path sets a
	// short value so a contended `git pull` yields fast instead of stalling.
	RunLockTimeout time.Duration
}

// Request is one sync/validate invocation. The caller is responsible for
// discovering adapters and materializing+decoding the IR (see Materialize)
// so the engine itself performs no network or git I/O — it is the pure
// write-pipeline orchestrator.
type Request struct {
	// Root is the opened workspace root; all staging/swap/ledger writes
	// go through it.
	Root *fsroot.Root

	// WorkspacePath is the absolute workspace path, for the report header.
	WorkspacePath string

	// Scope is the hierarchy level being emitted ("user", "project", or
	// "directory"), passed to each adapter session so it can pick
	// scope-appropriate output paths. Empty ⇒ "project" (back-compat).
	Scope string

	// Registry holds the discovered adapters.
	Registry *adapter.Registry

	// Targets is the ordered list of target names to sync (the manifest's
	// targets, before TargetsFilter is applied).
	Targets []string

	// Nodes is the decoded IR; Skills supplies skill asset bundles by node ID.
	Nodes  []ir.Node
	Skills map[string]ir.Skill

	// Commit is the resolved canonical SHA, stamped into staging metadata
	// and the report.
	Commit string

	Options Options
}

// TargetChange is one target's dry-run result (the Plan path).
type TargetChange struct {
	Target      string
	WouldCreate []string
	WouldUpdate []string
	WouldDelete []string
	Warnings    []string
	Error       string
	OutOfBand   []string // paths whose on-disk hash diverges from the ledger
}

// PlanResult is the result of a dry-run (validate). DriftDetected is true
// when any target has a non-empty change set or out-of-band modification.
type PlanResult struct {
	WorkspacePath string
	Commit        string
	Targets       []TargetChange
	DriftDetected bool
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) mode() report.Mode {
	if o.Mode == report.ModeBestEffort {
		return report.ModeBestEffort
	}
	return report.ModeAtomic
}

func (o Options) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	// Quiet by default: a library should not write to stderr unless the
	// caller (the CLI layer) injects a logger. Per AGENTS.md, when logs
	// do flow they go to stderr — that is the injected logger's job.
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
