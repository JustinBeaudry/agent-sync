package sync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDrift_RogueFileRefused(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	if err := os.MkdirAll(filepath.Join(ws, testPrefix), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	managed := testPrefix + "/rule.md"
	rogue := testPrefix + "/rogue.md"
	for _, p := range []string{managed, rogue} {
		if err := os.WriteFile(filepath.Join(ws, p), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	l := led(managed) // ledger knows only the managed file
	err := ScanDrift(root, testPrefix, l)
	if !errors.Is(err, ErrMidLifeDrift) {
		t.Fatalf("err = %v want ErrMidLifeDrift", err)
	}
	if !strings.Contains(err.Error(), "rogue.md") {
		t.Errorf("error should name the rogue file: %v", err)
	}
}

func TestScanDrift_AllManagedPasses(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	if err := os.MkdirAll(filepath.Join(ws, testPrefix), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	managed := testPrefix + "/rule.md"
	if err := os.WriteFile(filepath.Join(ws, managed), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := ScanDrift(root, testPrefix, led(managed)); err != nil {
		t.Errorf("all-managed prefix should pass: %v", err)
	}
}

func TestScanDrift_LedgerEntryMissingOnDiskIsNotDrift(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	if err := os.MkdirAll(filepath.Join(ws, testPrefix), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Ledger references a file that does not exist on disk (out-of-band
	// delete). That is a sync no-op, not drift.
	if err := ScanDrift(root, testPrefix, led(testPrefix+"/deleted.md")); err != nil {
		t.Errorf("missing-on-disk ledger entry should not be drift: %v", err)
	}
}
