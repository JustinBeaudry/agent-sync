package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRepo captures the state of a temporary repository fixture.
//
// Path is the filesystem path of a non-bare repository that tests can
// hand to Clone / LsRemote / Fetch as the "remote" URL. Absolute-path
// local URLs are interpreted by git as the file transport, which keeps
// tests hermetic (no network, no test servers, no binary fixtures
// committed to this repo).
type testRepo struct {
	Path       string
	InitialSHA string
	HeadBranch string
	SecondSHA  string
	TagName    string
	TagSHA     string
}

// mustGit runs a `git` subcommand in dir with the test-harness env.
// Test identities are forced on every invocation so the host
// environment's ~/.gitconfig cannot bleed in.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=aienvs-test",
		"GIT_AUTHOR_EMAIL=test@agent-sync.invalid",
		"GIT_COMMITTER_NAME=aienvs-test",
		"GIT_COMMITTER_EMAIL=test@agent-sync.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeRepo creates a new repository on disk with two commits on main
// and one annotated tag pointing at the first commit. It returns a
// fully-populated testRepo for assertions.
func makeRepo(t *testing.T) testRepo {
	t.Helper()
	dir := t.TempDir()

	mustGit(t, dir, "init", "--initial-branch=main", "--quiet")
	mustGit(t, dir, "config", "user.email", "test@agent-sync.invalid")
	mustGit(t, dir, "config", "user.name", "aienvs-test")
	// Ensure the post-init working tree is clean of `init.defaultBranch`
	// warnings so CombinedOutput stays quiet.
	mustGit(t, dir, "config", "init.defaultBranch", "main")

	write(t, filepath.Join(dir, "AGENTS.md"), "# agents\nline 1\n")
	mustGit(t, dir, "add", "AGENTS.md")
	mustGit(t, dir, "commit", "--quiet", "-m", "initial")
	sha1 := mustGit(t, dir, "rev-parse", "HEAD")

	write(t, filepath.Join(dir, "AGENTS.md"), "# agents\nline 1\nline 2\n")
	mustGit(t, dir, "add", "AGENTS.md")
	mustGit(t, dir, "commit", "--quiet", "-m", "second")
	sha2 := mustGit(t, dir, "rev-parse", "HEAD")

	mustGit(t, dir, "tag", "-a", "v1", "-m", "v1", sha1)

	return testRepo{
		Path:       dir,
		InitialSHA: sha1,
		HeadBranch: "main",
		SecondSHA:  sha2,
		TagName:    "v1",
		TagSHA:     sha1,
	}
}

// addShadowingTag creates a lightweight tag whose name matches the
// repo's primary branch (`HeadBranch`) but points at the initial commit.
// After this runs the repository has both `refs/heads/<name>` (at the
// branch tip, e.g. SecondSHA) and `refs/tags/<name>` (at InitialSHA), so
// the bare name is ambiguous to `git merge-base --is-ancestor`. Used by
// the reachability tag-shadow regression test.
func (r *testRepo) addShadowingTag(t *testing.T) {
	t.Helper()
	mustGit(t, r.Path, "tag", r.HeadBranch, r.InitialSHA)
}

// forcePush rewrites the given branch to a new commit that is NOT an
// ancestor of the previous tip. This simulates a force-push for the
// reachability-check test.
func (r *testRepo) forcePushDivergent(t *testing.T) string {
	t.Helper()
	// Create an orphan commit on a scratch branch, then reset the
	// original branch to it. The result: the original SHA is no longer
	// reachable from the branch ref.
	mustGit(t, r.Path, "checkout", "--quiet", "--orphan", "scratch")
	// Clear the index so the new commit is truly orphan with different
	// content.
	mustGit(t, r.Path, "rm", "-rf", "--quiet", ".")
	write(t, filepath.Join(r.Path, "OTHER.md"), "divergent\n")
	mustGit(t, r.Path, "add", "OTHER.md")
	mustGit(t, r.Path, "commit", "--quiet", "-m", "divergent")
	divergent := mustGit(t, r.Path, "rev-parse", "HEAD")

	mustGit(t, r.Path, "checkout", "--quiet", "-B", r.HeadBranch)
	mustGit(t, r.Path, "branch", "--quiet", "-D", "scratch")
	return divergent
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// requireGit skips the test if `git` is not available on PATH, so the
// test binary still passes on hosts without git installed. Most CI
// runners ship git; this is a belt-and-suspenders guard. When
// AGENT_SYNC_REQUIRE_GIT is set (CI), a missing git is a hard failure
// instead of a skip, so git-backed tests can never silently vanish.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("AGENT_SYNC_REQUIRE_GIT") != "" {
			t.Fatalf("git not available on PATH but AGENT_SYNC_REQUIRE_GIT is set: %v", err)
		}
		t.Skipf("git not available on PATH: %v", err)
	}
}

// withDetectReset ensures DetectGit's memo is cleared before and after
// the test so AIENVS_GIT_EXECUTABLE overrides take effect.
func withDetectReset(t *testing.T) {
	t.Helper()
	resetDetectForTests()
	t.Cleanup(resetDetectForTests)
}

// ctx returns a test-scoped context that is cancelled at cleanup.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
