package cli

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

func watchRC(ws string) *runtimeContext {
	return &runtimeContext{
		Access: Access{NonInteractive: true},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Flags:  PersistentFlags{Workspace: ws},
		Deps:   RootDeps{Out: io.Discard, Err: io.Discard},
	}
}

func TestRunWatchSync_SuccessClearsMarker(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	// Pre-seed a stale failure marker; a successful sync must clear it.
	writeWatchFailed(ws, time.Unix(0, 0).UTC(), errStub("stale"))
	marker := filepath.Join(ws, lastWatchFailedRel)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("precondition: marker should exist: %v", err)
	}

	wsObj, err := workspace.Find(ws, workspace.Options{Workspace: ws})
	if err != nil {
		t.Fatalf("workspace.Find: %v", err)
	}
	deps := RootDeps{Out: io.Discard, Err: io.Discard}
	if err := runWatchSync(context.Background(), watchRC(ws), deps, wsObj); err != nil {
		t.Fatalf("runWatchSync: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker cleared, stat err = %v", err)
	}
}

func TestRunWatchSync_FailureWritesMarker(t *testing.T) {
	requireGit(t)
	// A workspace dir with no manifest makes prepareEngine fail; runWatchSync
	// must write the failure marker and return the error.
	ws := t.TempDir()
	wsObj := &workspace.Workspace{Root: ws}
	deps := RootDeps{Out: io.Discard, Err: io.Discard}

	err := runWatchSync(context.Background(), watchRC(ws), deps, wsObj)
	if err == nil {
		t.Fatal("expected error when no manifest is present")
	}
	// The composed runWatchSync -> writeWatchFailed path must record the cause,
	// not just create an empty marker file.
	body, readErr := os.ReadFile(filepath.Join(ws, lastWatchFailedRel))
	if readErr != nil {
		t.Fatalf("expected failure marker written: %v", readErr)
	}
	if !strings.Contains(string(body), err.Error()) {
		t.Fatalf("marker body %q should contain the failure cause %q", body, err.Error())
	}
}

func TestWriteAndClearWatchFailed(t *testing.T) {
	ws := t.TempDir()
	marker := filepath.Join(ws, lastWatchFailedRel)

	writeWatchFailed(ws, time.Unix(0, 0).UTC(), errStub("boom"))
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("expected marker file: %v", err)
	}
	if !strings.Contains(string(body), "boom") {
		t.Fatalf("marker should contain cause, got %q", body)
	}

	clearWatchFailed(ws)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker removed, stat err = %v", err)
	}
	// clearWatchFailed on an already-clean workspace is a no-op (no panic).
	clearWatchFailed(ws)
}

func TestNewWatchCommand_Shape(t *testing.T) {
	cmd := newWatchCommand(RootDeps{})
	if cmd.Use != "watch" {
		t.Fatalf("Use = %q, want watch", cmd.Use)
	}
	if cmd.Flags().Lookup("debounce-ms") == nil {
		t.Fatal("expected --debounce-ms flag")
	}
}

func TestBundledTargetNames(t *testing.T) {
	names := bundledTargetNames()
	if len(names) == 0 {
		t.Fatal("expected at least one bundled target")
	}
	want := map[string]bool{"claude": false, "cursor": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, seen := range want {
		if !seen {
			t.Errorf("bundled target %q not listed in %v", n, names)
		}
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
