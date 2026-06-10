package gittest

import "testing"

func TestRequireGit_PresentDoesNotSkip(t *testing.T) {
	// On any host/CI with git installed this returns without skipping; the
	// test passing (not skipped) is the assertion. The skip/fatal branch is
	// exercised implicitly by the AGENT_SYNC_REQUIRE_GIT guard in CI.
	RequireGit(t)
}

func TestMustGit_RunsInHermeticEnv(t *testing.T) {
	RequireGit(t)
	dir := t.TempDir()
	MustGit(t, dir, "init", "--initial-branch=main", "--quiet")

	// Output is trimmed and the hermetic identity/config is in effect.
	inside := MustGit(t, dir, "rev-parse", "--is-inside-work-tree")
	if inside != "true" {
		t.Fatalf("rev-parse --is-inside-work-tree = %q, want %q", inside, "true")
	}

	// A committed file round-trips through the pinned test identity.
	MustGit(t, dir, "commit", "--allow-empty", "--quiet", "-m", "empty")
	author := MustGit(t, dir, "log", "-1", "--format=%an")
	if author != "agent-sync-test" {
		t.Fatalf("author = %q, want agent-sync-test (hermetic env not applied)", author)
	}
}
