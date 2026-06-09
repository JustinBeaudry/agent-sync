package ir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/git"
	"github.com/agent-sync/agent-sync/internal/gittest"
)

// canonicalRepo is a populated canonical-repo fixture for decoder tests.
// Path is on-disk; SHA is the resolved commit hash; Repo is opened against
// Path so test bodies can pass it directly to Decode.
type canonicalRepo struct {
	Path string
	SHA  string
	Repo *git.Repository
}

// canonicalFile describes one file to materialize inside the fixture.
// Mode defaults to 0o644 when zero.
type canonicalFile struct {
	Path    string // posix-style relative path within the canonical repo
	Content string
}

// makeCanonicalRepo builds a fresh canonical-repo fixture in t.TempDir().
// Each file is written, all are committed in one git commit, and the
// repo is opened via internal/git.Open. The git.Repository is closed
// automatically via t.Cleanup.
//
// File order in the slice is irrelevant — the caller can pass any order;
// the resulting tree's git-blob-keyed order is decoder-determined.
//
// requireGit() is called automatically; tests skip on hosts without git.
func makeCanonicalRepo(t *testing.T, files []canonicalFile) canonicalRepo {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", "--quiet")
	mustGit(t, dir, "config", "user.email", "test@agent-sync.invalid")
	mustGit(t, dir, "config", "user.name", "aienvs-test")
	mustGit(t, dir, "config", "init.defaultBranch", "main")

	for _, f := range files {
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
		mustGit(t, dir, "add", "--", filepath.FromSlash(f.Path))
	}

	if len(files) > 0 {
		mustGit(t, dir, "commit", "--quiet", "-m", "fixture")
	} else {
		// An empty repo can't easily be tested via the same Open path —
		// Decode never gets called for an empty fixture. Make an empty
		// commit so the test still has a valid SHA.
		mustGit(t, dir, "commit", "--allow-empty", "--quiet", "-m", "empty")
	}
	sha := mustGit(t, dir, "rev-parse", "HEAD")

	repo, err := git.Open(dir)
	if err != nil {
		t.Fatalf("git.Open(%q): %v", dir, err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	return canonicalRepo{Path: dir, SHA: sha, Repo: repo}
}

// mustGit runs `git <args>` in dir with hermetic env. Delegates to the shared
// gittest helper; kept as a package-local alias so this package's many call
// sites stay unchanged.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.MustGit(t, dir, args...)
}

// requireGit skips the test if `git` is not on PATH. Most CI runners
// have git; this is a belt-and-suspenders guard for development hosts.
// When AGENT_SYNC_REQUIRE_GIT is set (CI), a missing git is a hard failure
// instead of a skip, so git-backed tests can never silently vanish.
func requireGit(t *testing.T) {
	t.Helper()
	gittest.RequireGit(t)
}
