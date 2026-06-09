package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_NoPaths(t *testing.T) {
	if err := Run(context.Background(), Config{OnChange: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("expected error when no paths are configured")
	}
}

func TestRun_NoOnChange(t *testing.T) {
	if err := Run(context.Background(), Config{Paths: []string{"."}}); err == nil {
		t.Fatal("expected error when OnChange is nil")
	}
}

func TestRun_CancelledContextReturnsNil(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "m.yaml")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: Run sets up the watcher then exits cleanly.

	err := Run(ctx, Config{Paths: []string{f}, OnChange: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatalf("Run with cancelled ctx = %v, want nil", err)
	}
}
