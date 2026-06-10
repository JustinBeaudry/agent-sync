package sync

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnose_Unavailable(t *testing.T) {
	t.Parallel()
	d := Diagnose("/some/path")
	if d.Available {
		t.Error("Diagnose should report unavailable (stub / non-windows)")
	}
	if d.Note == "" {
		t.Error("Diagnose should carry an explanatory note")
	}
}

func TestStage_RequiresMeta(t *testing.T) {
	t.Parallel()
	root, _ := newWS(t)
	if _, err := Stage(root, testParent, testLeaf, Meta{}); err == nil {
		t.Error("Stage with empty Meta should error")
	}
	if _, err := Stage(root, testParent, testLeaf, Meta{Timestamp: "t"}); err == nil {
		t.Error("Stage with empty SHA should error")
	}
}

// TestRecover_ImpossibleStateIsLoggedNotActed asserts the defensive
// intend+.old shape is surfaced for an operator, never destructively
// guessed at.
func TestRecover_ImpossibleStateIsLoggedNotActed(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	writePrefix(t, ws, "LIVE")
	m := Meta{Timestamp: "20260608T020001Z", SHA: "imp0001"}
	s := stageGen(t, root, ws, "PARTIAL", m)
	s.Status = StatusIntend
	if err := writeSentinel(root, testSentinelRel(m), s); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	// Create the impossible companion: a leftover prefix.old alongside intend.
	if err := os.MkdirAll(filepath.Join(ws, testPrefix+".old"), 0o755); err != nil {
		t.Fatalf("seed .old: %v", err)
	}

	events, err := Recover(root, testParent)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Action, "impossible") {
		t.Errorf("expected an impossible-state event, got %+v", events)
	}
	// Nothing destructive happened: staging + .old + live prefix all intact.
	genDir := filepath.Join(ws, testParent, ".agent-sync-staging", m.Timestamp+"-"+m.SHA)
	if _, e := os.Stat(genDir); e != nil {
		t.Errorf("staging should be left intact for operator: %v", e)
	}
	if got := readPrefixFile(t, ws); got != "LIVE" {
		t.Errorf("live prefix disturbed: %q", got)
	}
}

func TestRecover_UnreadableSentinelRequiresIntervention(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	gen := "20260608T020002Z-bad0001"
	genDir := filepath.Join(ws, testParent, ".agent-sync-staging", gen)
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, sentinelPrefix+testLeaf), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed bad sentinel: %v", err)
	}
	events, err := Recover(root, testParent)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Action, "unreadable") {
		t.Errorf("expected unreadable-sentinel event, got %+v", events)
	}
	// Staging left intact.
	if _, e := os.Stat(filepath.Join(genDir, sentinelPrefix+testLeaf)); e != nil {
		t.Errorf("staging should be untouched: %v", e)
	}
}

func TestStagingGenRel_Shape(t *testing.T) {
	t.Parallel()
	rel := stagingGenRel(testParent, Meta{Timestamp: "T", SHA: "S"})
	if rel != path.Join(testParent, ".agent-sync-staging", "T-S") {
		t.Errorf("unexpected staging gen rel: %s", rel)
	}
}
