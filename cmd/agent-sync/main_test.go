package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// run() is the testable seam; main() only wraps it with os.Exit. These tests
// assert the argument-to-exit-code wiring (fang.Execute + cli.MapExit) without
// asserting on fang's styled output bytes, which are intentionally brittle.

func TestRun_Help(t *testing.T) {
	if code := run([]string{"--help"}); code != 0 {
		t.Fatalf("run --help = %d, want 0", code)
	}
}

func TestRun_Version(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("run --version = %d, want 0", code)
	}
}

// TestRun_VersionOutputUsesInjectedVersion guards the -ldflags -X main.version
// wiring. fang.Execute overwrites cobra's root.Version with its own
// buildVersion(), so the injected value only reaches `--version` when it is
// passed back through fang.WithVersion. Without that, a release binary silently
// reports fang's "unknown (built from source)" placeholder instead of its tag.
func TestRun_VersionOutputUsesInjectedVersion(t *testing.T) {
	orig := version
	version = "9.9.9-test"
	t.Cleanup(func() { version = orig })

	out := captureStdout(t, func() {
		if code := run([]string{"--version"}); code != 0 {
			t.Fatalf("run --version = %d, want 0", code)
		}
	})

	if !strings.Contains(out, "9.9.9-test") {
		t.Fatalf("--version output = %q, want it to contain the injected version %q", out, "9.9.9-test")
	}
	if strings.Contains(out, "unknown (built from source)") {
		t.Fatalf("--version output = %q, still shows fang's placeholder", out)
	}
}

func TestRun_NoArgs(t *testing.T) {
	// Root has no RunE; with no args cobra prints help and returns nil.
	// Must not hang and must exit cleanly (non-interactive-safe).
	if code := run([]string{}); code != 0 {
		t.Fatalf("run (no args) = %d, want 0", code)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	code := run([]string{"definitely-not-a-command"})
	if code == 0 {
		t.Fatal("run with unknown command = 0, want non-zero (MapExit usage)")
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns whatever
// was written. run() resolves its output writer to os.Stdout (RootDeps.Out is
// nil in production), so this captures the version line cobra prints there.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}
