package main

import "testing"

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
