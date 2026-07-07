package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/internal/manifest"
)

// --- helpers -----------------------------------------------------------

// makeUpdateRepo builds a canonical git repo with a root commit plus one
// content commit, returning (dir, rootSHA, headSHA). The content commit
// carries a rules/ file so a sync against it produces real output.
func makeUpdateRepo(t *testing.T) (dir, rootSHA, headSHA string) {
	t.Helper()
	dir = t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "root")
	rootSHA = mustGit(t, dir, "rev-parse", "HEAD")

	ruleDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ruleDir, "no-fri.md"), []byte("No PRs on Friday.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "add rule")
	headSHA = mustGit(t, dir, "rev-parse", "HEAD")
	return dir, rootSHA, headSHA
}

// commitFile adds relpath with content on the current branch and returns
// the new HEAD SHA.
func commitFile(t *testing.T, dir, relpath, content, msg string) string {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", msg)
	return mustGit(t, dir, "rev-parse", "HEAD")
}

// writeUpdateWS writes a local_path manifest pinned at sha (ref optional)
// targeting claude, and returns the workspace dir.
func writeUpdateWS(t *testing.T, canonicalPath, sha, ref string) string {
	t.Helper()
	ws := t.TempDir()
	var b strings.Builder
	b.WriteString("version: 1\ncanonical:\n")
	b.WriteString("  local_path: " + canonicalPath + "\n")
	if ref != "" {
		b.WriteString("  ref: " + ref + "\n")
	}
	b.WriteString("  commit: " + sha + "\n")
	b.WriteString("trusted_sha: " + sha + "\n")
	b.WriteString("targets:\n  - claude\n")
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func writeUpdateWorkspaceComposeWS(t *testing.T, canonicalPath, sha, ref, scope string) string {
	t.Helper()
	ws := t.TempDir()
	var b strings.Builder
	b.WriteString("version: 1\n")
	b.WriteString("scope: " + scope + "\n")
	b.WriteString("canonical:\n")
	b.WriteString("  local_path: " + canonicalPath + "\n")
	if ref != "" {
		b.WriteString("  ref: " + ref + "\n")
	}
	b.WriteString("  commit: " + sha + "\n")
	b.WriteString("trusted_sha: " + sha + "\n")
	b.WriteString("targets:\n")
	b.WriteString("  - cursor\n")
	b.WriteString("compose:\n")
	b.WriteString("  cursor-rules-from-user: true\n")
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func runUpdateCmd(t *testing.T, ws string, stdin string, extraArgs ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	if stdin != "" {
		root.SetIn(strings.NewReader(stdin))
	}
	args := append([]string{"update", "--workspace", ws}, extraArgs...)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func readManifest(t *testing.T, ws string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".agent-sync.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- tests -------------------------------------------------------------

func TestUpdate_UpToDate(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	before := readManifest(t, ws)

	out, errOut, err := runUpdateCmd(t, ws, "", "--non-interactive")
	if err != nil {
		t.Fatalf("update: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("expected up-to-date message, got %q", out)
	}
	if !bytes.Equal(before, readManifest(t, ws)) {
		t.Error("manifest changed on an up-to-date update")
	}
}

func TestUpdate_NewerCommit_NonInteractiveNeedsFlag(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	commitFile(t, canonical, "rules/more.md", "More rules.\n", "second rule")
	before := readManifest(t, ws)

	_, _, err := runUpdateCmd(t, ws, "", "--non-interactive")
	if err == nil {
		t.Fatal("expected fail-fast without --accept-update")
	}
	if code := MapExit(err); code != 4 {
		t.Errorf("exit code = %d, want 4 (trust-decision-required family)", code)
	}
	if !strings.Contains(err.Error(), "--accept-update") {
		t.Errorf("error should name --accept-update, got %v", err)
	}
	if !bytes.Equal(before, readManifest(t, ws)) {
		t.Error("manifest changed on a refused update")
	}
}

func TestUpdate_NewerCommit_AcceptFlagProceeds(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	newSHA := commitFile(t, canonical, "rules/more.md", "More rules.\n", "second rule")

	out, errOut, err := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-update="+newSHA)
	if err != nil {
		t.Fatalf("update: %v\nstderr: %s", err, errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+newSHA) {
		t.Errorf("manifest commit not re-pinned to %s:\n%s", newSHA, man)
	}
	if !strings.Contains(man, "trusted_sha: "+newSHA) {
		t.Errorf("trusted_sha not re-pinned to %s:\n%s", newSHA, man)
	}
	// Sync ran: the new rule file landed.
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "more.md")); statErr != nil {
		t.Errorf("expected synced rule from new commit: %v", statErr)
	}
	_ = out
}

func TestUpdate_NonFastForward_Refused(t *testing.T) {
	requireGit(t)
	canonical, rootSHA, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	// Rewrite history: reset main back to root, commit anew. head is now
	// orphaned, so it is not an ancestor of the new tip.
	mustGit(t, canonical, "reset", "--hard", rootSHA)
	rewritten := commitFile(t, canonical, "rules/rewritten.md", "Rewritten.\n", "rewritten history")
	before := readManifest(t, ws)

	// Routine acceptance flag must NOT override a rewrite.
	_, _, err := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-update="+rewritten)
	if err == nil {
		t.Fatal("expected refusal on non-fast-forward with only --accept-update")
	}
	if !strings.Contains(err.Error(), "rewritten") {
		t.Errorf("error should mention rewritten history, got %v", err)
	}
	if !bytes.Equal(before, readManifest(t, ws)) {
		t.Error("manifest changed on a refused non-fast-forward update")
	}

	// The distinct override flag proceeds.
	_, errOut, err := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-rewritten-history="+rewritten)
	if err != nil {
		t.Fatalf("override should proceed: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(string(readManifest(t, ws)), "commit: "+rewritten) {
		t.Error("manifest not re-pinned after rewrite override")
	}
}

func TestUpdate_ConcurrentLockRefuses(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	commitFile(t, canonical, "rules/more.md", "More.\n", "second")
	before := readManifest(t, ws)

	// Hold the workspace run lock, then run update: it must refuse up front.
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	rl, err := locks.NewRunLock(root)
	if err != nil {
		t.Fatal(err)
	}
	release, err := rl.Acquire(context.Background(), locks.AcquireOpts{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })

	_, _, uerr := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-update=any")
	if uerr == nil {
		t.Fatal("expected refusal while the run lock is held")
	}
	if !strings.Contains(uerr.Error(), "another agent-sync run") {
		t.Errorf("error should explain lock contention, got %v", uerr)
	}
	if !bytes.Equal(before, readManifest(t, ws)) {
		t.Error("manifest changed while another run held the lock")
	}
}

func TestUpdate_OfflineURLRefusesUpFront(t *testing.T) {
	ws := t.TempDir()
	man := "version: 1\ncanonical:\n  url: https://github.com/example/agent-config\n" +
		"  commit: 1111111111111111111111111111111111111111\n" +
		"trusted_sha: 1111111111111111111111111111111111111111\n" +
		"targets:\n  - claude\n"
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readManifest(t, ws)

	_, _, err := runUpdateCmd(t, ws, "", "--non-interactive", "--offline")
	if err == nil {
		t.Fatal("expected --offline refusal for a URL source")
	}
	if !strings.Contains(err.Error(), "offline") {
		t.Errorf("error should mention offline, got %v", err)
	}
	if !bytes.Equal(before, readManifest(t, ws)) {
		t.Error("manifest changed on an offline refusal")
	}
}

func TestUpdate_LocalDirNothingToPin(t *testing.T) {
	ws := t.TempDir()
	man := "version: 1\ncanonical:\n  local_dir: .agents\ntargets:\n  - claude\n"
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runUpdateCmd(t, ws, "", "--non-interactive")
	if err != nil {
		t.Fatalf("update: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "no pin to update") {
		t.Errorf("expected local_dir no-pin message, got %q", out)
	}
}

func TestUpdate_WorkspaceManifest_DoesNotComposeAsProject(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWorkspaceComposeWS(t, canonical, head, "main", manifest.ScopeWorkspace)
	home := t.TempDir()

	prevResolveHome := resolveHome
	resolveHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { resolveHome = prevResolveHome })

	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/from-user.md", "from user\n")

	newSHA := commitFile(t, canonical, "rules/after-update.md", "from update\n", "added rule")
	out, errOut, err := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-update="+newSHA)
	if err != nil {
		t.Fatalf("update: %v\nstdout: %s\nstderr: %s", err, out, errOut)
	}

	if _, err := os.Stat(filepath.Join(ws, ".cursor", "rules", "agent-sync", "after-update.mdc")); err != nil {
		t.Fatalf("expected project-composed rule output: %v", err)
	}
	composedPath := filepath.Join(ws, ".cursor", "rules", "agent-sync", "from-user.mdc")
	mustNotExist(t, composedPath)
}

func TestUpdate_MissingRefFollowsHead(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "") // no ref
	newSHA := commitFile(t, canonical, "rules/more.md", "More.\n", "second")

	out, errOut, err := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-update="+newSHA)
	if err != nil {
		t.Fatalf("update: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(string(readManifest(t, ws)), "commit: "+newSHA) {
		t.Error("HEAD-following update did not re-pin")
	}
	_ = out
}

func TestUpdate_PostRepinSyncFailure(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	// New commit whose tree has a malformed skill (a skills/<id>/ dir with
	// no SKILL.md), so re-pin succeeds but decode fails during the sync.
	newSHA := commitFile(t, canonical, "skills/broken/oops.txt", "not a skill\n", "broken skill")

	_, _, err := runUpdateCmd(t, ws, "", "--non-interactive", "--accept-update="+newSHA)
	if err == nil {
		t.Fatal("expected the post-re-pin sync to fail on the malformed skill")
	}
	if code := MapExit(err); code != exitUpdatePinMoved {
		t.Errorf("exit code = %d, want %d (pin-moved)", code, exitUpdatePinMoved)
	}
	if !strings.Contains(err.Error(), "agent-sync sync") {
		t.Errorf("error should give the re-run remediation, got %v", err)
	}
	// The manifest WAS re-pinned (the pin moved; only the files did not land).
	if !strings.Contains(string(readManifest(t, ws)), "commit: "+newSHA) {
		t.Error("manifest should show the moved pin even though sync failed")
	}
}

// TestConfirmUpdate_InteractiveGate exercises the interactive gate directly
// (the full command forces non-interactive without a TTY): it must render
// old→new SHAs, the ref, and the change summary, and honor a "y" answer.
func TestConfirmUpdate_InteractiveGate(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	newSHA := commitFile(t, canonical, "rules/more.md", "More.\n", "add more rules")

	m, err := manifest.LoadFile(filepath.Join(writeUpdateWS(t, canonical, head, "main"), ".agent-sync.yaml"),
		manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	res := updateResolution{newSHA: newSHA, refName: "main", mirrorPath: canonical}

	cmd := newUpdateCommand(RootDeps{})
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("y\n"))
	rc := &runtimeContext{Access: Access{NonInteractive: false}}

	ok, err := confirmUpdate(cmd, rc, m, head, res, true, updateFlags{})
	if err != nil {
		t.Fatalf("confirmUpdate: %v", err)
	}
	if !ok {
		t.Error("expected confirmation on 'y'")
	}
	got := out.String()
	for _, want := range []string{head[:12], newSHA[:12], "main", "add more rules"} {
		if !strings.Contains(got, want) {
			t.Errorf("gate output missing %q:\n%s", want, got)
		}
	}
}
