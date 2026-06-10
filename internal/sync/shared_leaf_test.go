package sync

import (
	"os"
	"path"
	"path/filepath"
	"testing"
)

// TestRecover_MultiLeafSentinelsDoNotCollide is the regression guard for the
// per-leaf sentinel fix: two leaves staged into ONE generation dir (as a
// shared-subdir sync does) must each get an independent ".state-<leaf>"
// sentinel, so one leaf's swap/recovery can never clobber another's record.
func TestRecover_MultiLeafSentinelsDoNotCollide(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)

	m := Meta{Timestamp: "20260610T010001Z", SHA: "leaf0001"}
	leafA, err := Stage(root, testParent, "aienvs-a", m)
	if err != nil {
		t.Fatalf("stage A: %v", err)
	}
	leafB, err := Stage(root, testParent, "aienvs-b", m)
	if err != nil {
		t.Fatalf("stage B: %v", err)
	}

	// Distinct sentinel paths in the same generation dir — the core property.
	relA, relB := sentinelRelFor(leafA), sentinelRelFor(leafB)
	if relA == relB {
		t.Fatalf("leaf sentinels collide: %q == %q", relA, relB)
	}
	if path.Dir(relA) != path.Dir(relB) {
		t.Fatalf("expected both sentinels in one gen dir; got %q vs %q", relA, relB)
	}

	prefixA := testParent + "/aienvs-a"
	prefixB := testParent + "/aienvs-b"
	// Seed: leaf A at step2_done with a lingering .old (cleanup-needed);
	// leaf B at intend (crash before step1, staging to discard).
	if err := os.MkdirAll(filepath.Join(ws, prefixA+".old"), 0o755); err != nil {
		t.Fatalf("seed A .old: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, prefixA), 0o755); err != nil {
		t.Fatalf("seed A prefix: %v", err)
	}
	if err := writeSentinel(root, relA, Sentinel{Status: StatusStep2Done, PrefixRel: prefixA, StagingLeafRel: leafA, SHA: m.SHA, StartedAt: m.Timestamp}); err != nil {
		t.Fatalf("seed sentinel A: %v", err)
	}
	if err := writeSentinel(root, relB, Sentinel{Status: StatusIntend, PrefixRel: prefixB, StagingLeafRel: leafB, SHA: m.SHA, StartedAt: m.Timestamp}); err != nil {
		t.Fatalf("seed sentinel B: %v", err)
	}

	events, err := Recover(root, testParent)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 reconcile events (one per leaf), got %d: %+v", len(events), events)
	}

	// A: .old cleaned, sentinel removed, live prefix kept.
	if _, e := os.Stat(filepath.Join(ws, prefixA+".old")); !os.IsNotExist(e) {
		t.Errorf("leaf A .old not cleaned: %v", e)
	}
	if _, e := os.Stat(filepath.Join(ws, relA)); !os.IsNotExist(e) {
		t.Errorf("leaf A sentinel not removed: %v", e)
	}
	if _, e := os.Stat(filepath.Join(ws, prefixA)); e != nil {
		t.Errorf("leaf A live prefix should survive: %v", e)
	}
	// B: intend staging discarded, sentinel removed.
	if _, e := os.Stat(filepath.Join(ws, leafB)); !os.IsNotExist(e) {
		t.Errorf("leaf B intend staging not discarded: %v", e)
	}
	if _, e := os.Stat(filepath.Join(ws, relB)); !os.IsNotExist(e) {
		t.Errorf("leaf B sentinel not removed: %v", e)
	}
}
