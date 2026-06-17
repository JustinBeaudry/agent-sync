package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/workspace"
)

// lastWatchFailedRel is the marker watch mode writes on failure (U9). When
// present, status surfaces it as a banner.
const lastWatchFailedRel = ".agent-sync/state/last-watch.failed"

// statusReport is the read-only status document.
type statusReport struct {
	Workspace   string              `json:"workspace"`
	Manifest    string              `json:"manifest"`
	Source      string              `json:"source"`
	Commit      string              `json:"commit"`
	Targets     []targetStatusEntry `json:"targets"`
	WatchFailed bool                `json:"watch_failed"`
}

type targetStatusEntry struct {
	Target  string `json:"target"`
	Managed int    `json:"managed_files"`
	Tracked bool   `json:"tracked"` // has a ledger
}

// hierarchyStatusReport is the multi-scope status document emitted when status
// runs across a discovered hierarchy (no --workspace override). It lists every
// scope read-only: the user scope (Emit=false), the project scope, and any
// nested directory scopes.
type hierarchyStatusReport struct {
	Scopes []scopeStatus `json:"scopes"`
}

// scopeStatus is one discovered scope's read-only status: its level, root,
// manifest source, whether it is read-only (the user scope), and per-target
// managed-file counts loaded from that scope's own ledger. Err is set (and the
// other fields beyond Level/Root are empty) when the scope's manifest fails to
// load — status continues past it (continue-and-report).
type scopeStatus struct {
	Level       string              `json:"level"`
	Root        string              `json:"root"`
	Source      string              `json:"source,omitempty"`
	ReadOnly    bool                `json:"read_only"`
	Targets     []targetStatusEntry `json:"targets,omitempty"`
	WatchFailed bool                `json:"watch_failed"`
	Err         string              `json:"error,omitempty"`
}

func newStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show workspace, manifest, and per-target sync state",
		Long:          "Report the resolved workspace, the manifest's canonical source and pin, and each target's managed-file count. Read-only.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := mustRuntime(cmd)
			if err != nil {
				return err
			}
			flags := rc.Flags

			// No --workspace override: report every discovered scope read-only
			// (user/project/directory). An explicit --workspace pins a single
			// scope and keeps today's single-scope status.
			if flags.Workspace == "" {
				return runHierarchyStatus(cmd, rc)
			}

			ws, err := workspace.Find(flags.Workspace, workspace.Options{Workspace: flags.Workspace})
			if err != nil {
				return fmt.Errorf("status: locate workspace: %w", err)
			}
			m, err := manifest.LoadFile(ws.ManifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
			if err != nil {
				return fmt.Errorf("status: load manifest: %w", err)
			}
			root, err := fsroot.OpenWorkspaceRoot(ws.Root)
			if err != nil {
				return fmt.Errorf("status: open workspace root: %w", err)
			}
			defer func() { _ = root.Close() }()

			rep := statusReport{
				Workspace:   ws.Root,
				Manifest:    ws.ManifestPath,
				Source:      sourceOf(m),
				Commit:      m.Canonical.Commit,
				WatchFailed: markerExists(root, lastWatchFailedRel),
			}
			for _, target := range sortedCopy(m.Targets) {
				entry := targetStatusEntry{Target: target}
				if led, lerr := ledger.Load(root, target); lerr == nil {
					entry.Tracked = true
					entry.Managed = len(led.Entries)
				} else if !errors.Is(lerr, ledger.ErrLedgerNotFound) {
					return fmt.Errorf("status: load ledger %q: %w", target, lerr)
				}
				rep.Targets = append(rep.Targets, entry)
			}

			if rc.Access.Output == OutputJSON {
				data, merr := json.MarshalIndent(rep, "", "  ")
				if merr != nil {
					return fmt.Errorf("status: marshal json: %w", merr)
				}
				_, werr := fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return werr
			}
			return renderStatusText(cmd, rep)
		},
	}
	return cmd
}

// runHierarchyStatus discovers every scope from cwd and reports each one
// read-only: it loads each scope's manifest for the source string, opens the
// scope root only to READ its ledgers (no staging/swap, no engine), and
// records per-target managed counts. The user scope is discovered Emit=false
// and listed as read-only. A scope whose manifest fails to load is recorded as
// an error line and the rest of the hierarchy is still reported.
func runHierarchyStatus(cmd *cobra.Command, rc *runtimeContext) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("status: resolve cwd: %w", err)
	}
	home, err := resolveHome()
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	scopes, err := hierarchy.Discover(cwd, hierarchy.Options{Home: home})
	if err != nil {
		return fmt.Errorf("status: discover hierarchy: %w", err)
	}

	rep := hierarchyStatusReport{Scopes: make([]scopeStatus, 0, len(scopes))}
	for _, sc := range scopes {
		st := scopeStatus{
			Level: sc.Level.String(),
			Root:  sc.Root,
			// The user scope is never emitted; surface it read-only.
			ReadOnly: !sc.Emit,
		}
		ss, serr := scopeTargets(rc, sc)
		if serr != nil {
			// Continue-and-report: record the per-scope error, keep going.
			st.Err = serr.Error()
			rep.Scopes = append(rep.Scopes, st)
			continue
		}
		st.Source = ss.Source
		st.Targets = ss.Targets
		st.WatchFailed = ss.WatchFailed
		rep.Scopes = append(rep.Scopes, st)
	}

	if rc.Access.Output == OutputJSON {
		data, merr := json.MarshalIndent(rep, "", "  ")
		if merr != nil {
			return fmt.Errorf("status: marshal json: %w", merr)
		}
		_, werr := fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return werr
	}
	return renderHierarchyStatusText(cmd, rep)
}

// scopeResult holds the manifest-derived facts for one scope.
type scopeResult struct {
	Source      string
	Targets     []targetStatusEntry
	WatchFailed bool
}

// scopeTargets loads a scope's manifest and, for each manifest target, reads
// that scope's own ledger to count managed files. The scope root is opened
// read-only solely to read ledgers and closed before returning.
func scopeTargets(rc *runtimeContext, sc hierarchy.Scope) (scopeResult, error) {
	m, err := manifest.LoadFile(sc.ManifestPath, manifest.LoadOptions{NonInteractive: rc.Access.NonInteractive})
	if err != nil {
		return scopeResult{}, fmt.Errorf("load manifest: %w", err)
	}
	root, err := fsroot.OpenWorkspaceRoot(sc.Root)
	if err != nil {
		return scopeResult{}, fmt.Errorf("open root: %w", err)
	}
	defer func() { _ = root.Close() }()

	res := scopeResult{Source: sourceOf(m), WatchFailed: markerExists(root, lastWatchFailedRel)}
	for _, target := range sortedCopy(m.Targets) {
		entry := targetStatusEntry{Target: target}
		if led, lerr := ledger.Load(root, target); lerr == nil {
			entry.Tracked = true
			entry.Managed = len(led.Entries)
		} else if !errors.Is(lerr, ledger.ErrLedgerNotFound) {
			return scopeResult{}, fmt.Errorf("load ledger %q: %w", target, lerr)
		}
		res.Targets = append(res.Targets, entry)
	}
	return res, nil
}

// renderHierarchyStatusText writes a per-scope block: a header naming the level
// and root (with a [read-only] tag for the user scope), then either the
// per-target lines or the scope's error line.
func renderHierarchyStatusText(cmd *cobra.Command, rep hierarchyStatusReport) error {
	w := cmd.OutOrStdout()
	for i, sc := range rep.Scopes {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		header := fmt.Sprintf("%s (%s)", sc.Level, sc.Root)
		if sc.ReadOnly {
			header += " [read-only]"
		}
		if _, err := fmt.Fprintln(w, header); err != nil {
			return err
		}
		if sc.Err != "" {
			if _, err := fmt.Fprintf(w, "  ERROR: %s\n", sc.Err); err != nil {
				return err
			}
			continue
		}
		if sc.WatchFailed {
			if _, err := fmt.Fprintln(w, "  WARNING: the last watch-mode sync failed (see .agent-sync/state/last-watch.failed)"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "  source: %s\n", sc.Source); err != nil {
			return err
		}
		for _, t := range sc.Targets {
			state := "untracked"
			if t.Tracked {
				state = fmt.Sprintf("%d managed file(s)", t.Managed)
			}
			if _, err := fmt.Fprintf(w, "  target %s: %s\n", t.Target, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderStatusText(cmd *cobra.Command, rep statusReport) error {
	w := cmd.OutOrStdout()
	if rep.WatchFailed {
		if _, err := fmt.Fprintln(w, "WARNING: the last watch-mode sync failed (see .agent-sync/state/last-watch.failed)"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "workspace: %s\nsource:    %s\ncommit:    %s\n", rep.Workspace, rep.Source, displayCommit(rep.Commit)); err != nil {
		return err
	}
	for _, t := range rep.Targets {
		state := "untracked"
		if t.Tracked {
			state = fmt.Sprintf("%d managed file(s)", t.Managed)
		}
		if _, err := fmt.Fprintf(w, "target %s: %s\n", t.Target, state); err != nil {
			return err
		}
	}
	return nil
}

func sourceOf(m *manifest.Manifest) string {
	switch {
	case m.Canonical.URL != "":
		return m.Canonical.URL
	case m.Canonical.LocalPath != "":
		return m.Canonical.LocalPath
	default:
		return m.Canonical.LocalDir
	}
}

func displayCommit(c string) string {
	if c == "" {
		return "(floating)"
	}
	return c
}

func markerExists(root *fsroot.Root, rel string) bool {
	_, err := root.Lstat(rel)
	return err == nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
