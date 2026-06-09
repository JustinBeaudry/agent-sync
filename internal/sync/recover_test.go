package sync

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"
)

func sentinelRelFor(m Meta) string {
	return path.Join(testParent, ".aienv-staging", m.Timestamp+"-"+m.SHA, ".state")
}

// TestRecover_CompletesStep1Done builds the on-disk shape of a crash
// between step 1 and step 2, then asserts Recover finishes the promotion
// to match a never-crashed run.
func TestRecover_CompletesStep1Done(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	writePrefix(t, ws, "OLD")
	m := Meta{Timestamp: "20260608T010001Z", SHA: "rec0001"}
	s := stageGen(t, root, ws, "NEW", m)

	// Simulate: step 1 done (prefix moved aside), crash before step 2.
	if err := os.Rename(filepath.Join(ws, testPrefix), filepath.Join(ws, testPrefix+".old")); err != nil {
		t.Fatalf("seed move-aside: %v", err)
	}
	s.Status = StatusStep1Done
	if err := writeSentinel(root, sentinelRelFor(m), s); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	events, err := Recover(root, testParent)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one recovery event")
	}
	if got := readPrefixFile(t, ws); got != "NEW" {
		t.Errorf("prefix content = %q want NEW (promotion not completed)", got)
	}
	if _, e := os.Stat(filepath.Join(ws, testPrefix+".old")); !errors.Is(e, os.ErrNotExist) {
		t.Errorf(".old not cleaned: %v", e)
	}
	if _, e := os.Stat(filepath.Join(ws, sentinelRelFor(m))); !errors.Is(e, os.ErrNotExist) {
		t.Errorf("sentinel not removed: %v", e)
	}
}

func TestRecover_Step2DoneCleansOld(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	writePrefix(t, ws, "NEW") // already promoted
	// Leftover .old + a step2_done sentinel.
	if err := os.MkdirAll(filepath.Join(ws, testPrefix+".old"), 0o755); err != nil {
		t.Fatalf("seed .old: %v", err)
	}
	m := Meta{Timestamp: "20260608T010002Z", SHA: "rec0002"}
	if _, err := Stage(root, testParent, testLeaf, m); err != nil {
		t.Fatalf("stage: %v", err)
	}
	s := Sentinel{Status: StatusStep2Done, PrefixRel: testPrefix, StagingLeafRel: path.Join(testParent, ".aienv-staging", m.Timestamp+"-"+m.SHA, testLeaf), SHA: m.SHA, StartedAt: m.Timestamp}
	if err := writeSentinel(root, sentinelRelFor(m), s); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	if _, err := Recover(root, testParent); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if _, e := os.Stat(filepath.Join(ws, testPrefix+".old")); !errors.Is(e, os.ErrNotExist) {
		t.Errorf(".old not cleaned: %v", e)
	}
	if got := readPrefixFile(t, ws); got != "NEW" {
		t.Errorf("prefix content changed: %q", got)
	}
}

func TestRecover_IntendDiscardsStaging(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	writePrefix(t, ws, "LIVE")
	m := Meta{Timestamp: "20260608T010003Z", SHA: "rec0003"}
	s := stageGen(t, root, ws, "PARTIAL", m)
	s.Status = StatusIntend
	if err := writeSentinel(root, sentinelRelFor(m), s); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	if _, err := Recover(root, testParent); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	genDir := filepath.Join(ws, testParent, ".aienv-staging", m.Timestamp+"-"+m.SHA)
	if _, e := os.Stat(genDir); !errors.Is(e, os.ErrNotExist) {
		t.Errorf("intend staging not discarded: %v", e)
	}
	if got := readPrefixFile(t, ws); got != "LIVE" {
		t.Errorf("live prefix disturbed: %q", got)
	}
}

func TestRecover_IdempotentOnCleanTree(t *testing.T) {
	t.Parallel()
	root, _ := newWS(t)
	for i := 0; i < 2; i++ {
		events, err := Recover(root, testParent)
		if err != nil {
			t.Fatalf("Recover run %d: %v", i, err)
		}
		if len(events) != 0 {
			t.Errorf("clean tree produced %d events", len(events))
		}
	}
}

func TestRecover_PrunesToLastThree(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	stagingRoot := filepath.Join(ws, testParent, ".aienv-staging")
	gens := []string{"g1", "g2", "g3", "g4", "g5"}
	for _, g := range gens {
		if err := os.MkdirAll(filepath.Join(stagingRoot, g), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", g, err)
		}
	}
	if _, err := Recover(root, testParent); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != generationsToKeep {
		t.Errorf("kept %d generations want %d", len(entries), generationsToKeep)
	}
	// Newest (g3,g4,g5) survive; g1,g2 pruned.
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	for _, want := range []string{"g3", "g4", "g5"} {
		if !got[want] {
			t.Errorf("expected %s to survive prune", want)
		}
	}
}

func TestCleanScratch(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	stagingRoot := filepath.Join(ws, testParent, ".aienv-staging")
	if err := os.MkdirAll(filepath.Join(stagingRoot, "g1"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := CleanScratch(root, testParent); err != nil {
		t.Fatalf("CleanScratch: %v", err)
	}
	if _, e := os.Stat(stagingRoot); !errors.Is(e, os.ErrNotExist) {
		t.Errorf("staging not cleared: %v", e)
	}
}

func TestSentinel_RoundTripAndStrictDecode(t *testing.T) {
	t.Parallel()
	root, _ := newWS(t)
	if err := os.MkdirAll(filepath.Join(root.Path(), testParent, ".aienv-staging", "g"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rel := path.Join(testParent, ".aienv-staging", "g", ".state")
	in := Sentinel{Status: StatusStep1Done, Workspace: root.Path(), Target: "claude", SHA: "x", StartedAt: "t", PrefixRel: testPrefix, StagingLeafRel: testPrefix}
	if err := writeSentinel(root, rel, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readSentinel(root, rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Status != in.Status || got.PrefixRel != in.PrefixRel {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Unknown status rejected.
	bad := filepath.Join(root.Path(), testParent, ".aienv-staging", "g", "bad.state")
	if err := os.WriteFile(bad, []byte(`{"status":"bogus"}`), 0o600); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	if _, err := readSentinel(root, path.Join(testParent, ".aienv-staging", "g", "bad.state")); err == nil {
		t.Error("expected error for unknown status")
	}
}
