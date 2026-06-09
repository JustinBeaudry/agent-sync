package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfig_Ignored(t *testing.T) {
	cfg := Config{IgnorePrefixes: []string{"/ws/.aienv"}}
	cases := []struct {
		path string
		want bool
	}{
		{"/ws/.aienv/state/claude.json", true},
		{"/ws/.aienv", true},
		{"/ws/.aienv.yaml", false}, // sibling, not under the prefix
		{"/ws/rules/x.md", false},
	}
	for _, c := range cases {
		if got := cfg.ignored(c.path); got != c.want {
			t.Errorf("ignored(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestRun_FiresOnChangeForFileEdit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(file, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	fired := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Paths:    []string{file, dir},
			Debounce: 40 * time.Millisecond,
			OnChange: func(context.Context) error {
				calls.Add(1)
				select {
				case fired <- struct{}{}:
				default:
				}
				return nil
			},
		})
	}()

	// Give the watcher a moment to register, then modify the file.
	time.Sleep(80 * time.Millisecond)
	if err := os.WriteFile(file, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-fired:
		// OnChange ran — good.
	case <-time.After(3 * time.Second):
		t.Fatal("OnChange did not fire within 3s of a file edit")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if calls.Load() == 0 {
		t.Fatal("expected at least one OnChange call")
	}
}

func TestRun_RequiresPathsAndOnChange(t *testing.T) {
	if err := Run(context.Background(), Config{OnChange: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("expected error with no paths")
	}
	if err := Run(context.Background(), Config{Paths: []string{"/x"}}); err == nil {
		t.Fatal("expected error with no OnChange")
	}
}

func TestRun_CancelReturnsPromptly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Paths:    []string{dir},
			OnChange: func(context.Context) error { return nil },
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}
}
