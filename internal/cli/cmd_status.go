package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/agent-sync/agent-sync/internal/fsroot"
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
	if m.Canonical.URL != "" {
		return m.Canonical.URL
	}
	return m.Canonical.LocalPath
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
