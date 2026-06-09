//go:build !windows

package workspace_test

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/agent-sync/agent-sync/internal/workspace"
)

func TestFind_ExplicitWorkspaceToFIFO(t *testing.T) {
	tmp := t.TempDir()
	fifoPath := filepath.Join(tmp, workspace.ManifestName)
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	_, err := workspace.Find(tmp, workspace.Options{Workspace: fifoPath})
	if err == nil {
		t.Fatal("expected error for FIFO manifest path; got nil")
	}
	if !errors.Is(err, workspace.ErrManifestNotRegular) {
		t.Errorf("expected ErrManifestNotRegular, got %v", err)
	}
}
