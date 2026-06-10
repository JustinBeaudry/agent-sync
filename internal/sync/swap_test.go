package sync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

const (
	testParent = ".claude/rules"
	testLeaf   = "aienvs"
	testPrefix = ".claude/rules/aienvs"
)

func newWS(t *testing.T) (*fsroot.Root, string) {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, testParent), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, ws
}

// writePrefix creates the live prefix dir with one file of the given content.
func writePrefix(t *testing.T, ws, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ws, testPrefix), 0o755); err != nil {
		t.Fatalf("mkdir prefix: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, testPrefix, "rule.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write prefix file: %v", err)
	}
}

// stageGen stages a generation and writes content into it; returns the sentinel.
func stageGen(t *testing.T, root *fsroot.Root, ws, content string, m Meta) Sentinel {
	t.Helper()
	leafRel, err := Stage(root, testParent, testLeaf, m)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, leafRel, "rule.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	return Sentinel{
		Workspace:      ws,
		Target:         "claude",
		SHA:            m.SHA,
		StartedAt:      m.Timestamp,
		PrefixRel:      testPrefix,
		StagingLeafRel: leafRel,
	}
}

func readPrefixFile(t *testing.T, ws string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, testPrefix, "rule.md"))
	if err != nil {
		t.Fatalf("read prefix file: %v", err)
	}
	return string(b)
}

func TestSwap_HappyPath(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	writePrefix(t, ws, "OLD")
	m := Meta{Timestamp: "20260608T000001Z", SHA: "abc1234"}
	s := stageGen(t, root, ws, "NEW", m)

	if err := Swap(root, s); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if got := readPrefixFile(t, ws); got != "NEW" {
		t.Errorf("prefix content = %q want NEW", got)
	}
	// .old gone, sentinel gone.
	if _, err := os.Stat(filepath.Join(ws, testPrefix+".old")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".old not cleaned: %v", err)
	}
	sentinelRel := filepath.Join(ws, testParent, ".aienv-staging", m.Timestamp+"-"+m.SHA, ".state")
	if _, err := os.Stat(sentinelRel); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sentinel not removed: %v", err)
	}
}

func TestSwap_FirstSyncNoPriorPrefix(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	// No prior prefix; step 1 is skipped.
	m := Meta{Timestamp: "20260608T000002Z", SHA: "def5678"}
	s := stageGen(t, root, ws, "FIRST", m)
	if err := Swap(root, s); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if got := readPrefixFile(t, ws); got != "FIRST" {
		t.Errorf("prefix content = %q want FIRST", got)
	}
}

func TestSwap_Step2FailureLeavesRecoverableShape(t *testing.T) {
	// NOT parallel: mutates the package-global renameStep seam. Running
	// in the sequential phase keeps it isolated from the parallel tests,
	// which run only after all non-parallel tests finish.
	root, ws := newWS(t)
	writePrefix(t, ws, "OLD")
	m := Meta{Timestamp: "20260608T000003Z", SHA: "fail999"}
	s := stageGen(t, root, ws, "NEW", m)

	// Inject a failure on the step-2 rename (target == prefix).
	orig := renameStep
	renameStep = func(r *fsroot.Root, oldRel, newRel string) error {
		if newRel == testPrefix {
			return errors.New("injected step2 failure")
		}
		return orig(r, oldRel, newRel)
	}
	defer func() { renameStep = orig }()

	err := Swap(root, s)
	if err == nil {
		t.Fatal("expected step2 failure")
	}
	// Recoverable shape: prefix absent, .old present, staging present, sentinel=step1_done.
	if _, e := os.Stat(filepath.Join(ws, testPrefix)); !errors.Is(e, os.ErrNotExist) {
		t.Errorf("prefix should be absent after step2 failure: %v", e)
	}
	if _, e := os.Stat(filepath.Join(ws, testPrefix+".old")); e != nil {
		t.Errorf(".old should be present: %v", e)
	}
	if _, e := os.Stat(filepath.Join(ws, s.StagingLeafRel)); e != nil {
		t.Errorf("staging should be present: %v", e)
	}
	sentinelRel := filepath.Join(testParent, ".aienv-staging", m.Timestamp+"-"+m.SHA, sentinelPrefix+testLeaf)
	got, e := readSentinel(root, sentinelRel)
	if e != nil {
		t.Fatalf("read sentinel: %v", e)
	}
	if got.Status != StatusStep1Done {
		t.Errorf("sentinel status = %q want step1_done", got.Status)
	}
}

func TestSwap_RefusesStalePrefixOld(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	writePrefix(t, ws, "OLD")
	// Leftover .old from a prior crash.
	if err := os.MkdirAll(filepath.Join(ws, testPrefix+".old"), 0o755); err != nil {
		t.Fatalf("seed .old: %v", err)
	}
	m := Meta{Timestamp: "20260608T000004Z", SHA: "stale01"}
	s := stageGen(t, root, ws, "NEW", m)
	if err := Swap(root, s); !errors.Is(err, ErrStale) {
		t.Errorf("Swap err = %v want ErrStale", err)
	}
}
