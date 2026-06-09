package sync

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aienvs/aienvs/internal/ledger"
)

func led(paths ...string) ledger.Ledger {
	l := ledger.Ledger{SchemaVersion: 1, Target: "claude"}
	for _, p := range paths {
		l.Entries = append(l.Entries, ledger.Entry{Path: p})
	}
	return l
}

func TestOrphans_DiffOnly(t *testing.T) {
	t.Parallel()
	old := led(".claude/rules/aienvs/a.md", ".claude/rules/aienvs/b.md", ".claude/rules/aienvs/c.md")
	next := led(".claude/rules/aienvs/a.md", ".claude/rules/aienvs/c.md")
	got := Orphans(old, next)
	want := []string{".claude/rules/aienvs/b.md"}
	if !slices.Equal(got, want) {
		t.Errorf("Orphans = %v want %v", got, want)
	}
	// New entries never produce orphans; identical ledgers → none.
	if g := Orphans(next, next); len(g) != 0 {
		t.Errorf("identical ledgers should yield no orphans, got %v", g)
	}
}

func TestDeleteOrphans_RemovesAndToleratesMissing(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	if err := os.MkdirAll(filepath.Join(ws, testPrefix), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	present := testPrefix + "/a.md"
	if err := os.WriteFile(filepath.Join(ws, present), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	missing := testPrefix + "/gone.md" // out-of-band deleted already
	deleted, err := DeleteOrphans(root, []string{present, missing})
	if err != nil {
		t.Fatalf("DeleteOrphans: %v", err)
	}
	if !slices.Equal(deleted, []string{present}) {
		t.Errorf("deleted = %v want [%s]", deleted, present)
	}
	if _, e := os.Stat(filepath.Join(ws, present)); !errors.Is(e, os.ErrNotExist) {
		t.Errorf("present orphan not removed: %v", e)
	}
}

func TestCheckExpectedDeletions(t *testing.T) {
	t.Parallel()
	if err := CheckExpectedDeletions(-1, 5); err != nil {
		t.Errorf("unspecified (-1) should pass: %v", err)
	}
	if err := CheckExpectedDeletions(2, 2); err != nil {
		t.Errorf("match should pass: %v", err)
	}
	if err := CheckExpectedDeletions(2, 3); !errors.Is(err, ErrDeletionCountMismatch) {
		t.Errorf("mismatch err = %v want ErrDeletionCountMismatch", err)
	}
}
