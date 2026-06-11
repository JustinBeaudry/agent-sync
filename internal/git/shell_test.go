package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectGit_HappyPath(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	if err := DetectGit(); err != nil {
		t.Fatalf("DetectGit: %v", err)
	}
	if GitPath() == "" {
		t.Fatal("GitPath empty after successful DetectGit")
	}
}

func TestDetectGit_OverrideMissing(t *testing.T) {
	withDetectReset(t)
	t.Setenv("AGENT_SYNC_GIT_EXECUTABLE", "/nonexistent/binary/that/does/not/exist")

	err := DetectGit()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrGitNotFound) {
		t.Fatalf("expected ErrGitNotFound, got %v", err)
	}
}

// TestDetectGit_OverrideDirectory confirms that the override is validated
// as runnable, not merely existent. Pointing AGENT_SYNC_GIT_EXECUTABLE at a
// directory must fail at detection time with ErrGitNotFound rather than
// deferring the failure to exec time. Under an `os.Stat` implementation
// this would erroneously succeed (directories stat fine); exec.LookPath
// refuses non-executables on Unix and PATHEXT-mismatches on Windows.
func TestDetectGit_OverrideDirectory(t *testing.T) {
	withDetectReset(t)
	t.Setenv("AGENT_SYNC_GIT_EXECUTABLE", t.TempDir())

	err := DetectGit()
	if err == nil {
		t.Fatal("expected error pointing override at a directory, got nil")
	}
	if !errors.Is(err, ErrGitNotFound) {
		t.Fatalf("expected ErrGitNotFound, got %v", err)
	}
}

func TestDetectGit_Idempotent(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	if err := DetectGit(); err != nil {
		t.Fatalf("DetectGit first call: %v", err)
	}
	if err := DetectGit(); err != nil {
		t.Fatalf("DetectGit second call: %v", err)
	}
}

func TestLsRemote_HappyPath(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)

	sha, err := LsRemote(testCtx(t), r.Path, r.HeadBranch)
	if err != nil {
		t.Fatalf("LsRemote: %v", err)
	}
	if sha != r.SecondSHA {
		t.Fatalf("LsRemote SHA = %q, want %q", sha, r.SecondSHA)
	}
}

func TestLsRemote_Tag(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)

	// Annotated tag: ls-remote reports the tag object's SHA in the
	// primary line; the dereferenced commit is reported with the `^{}`
	// suffix. LsRemote must return the dereferenced commit SHA so
	// downstream [Repository.ReadTree] (which goes through
	// CommitObject) can resolve it. r.TagSHA is the commit SHA the
	// annotated tag points at — that is what we expect back, NOT the
	// tag object SHA.
	sha, err := LsRemote(testCtx(t), r.Path, r.TagName)
	if err != nil {
		t.Fatalf("LsRemote tag: %v", err)
	}
	if !shaPattern.MatchString(sha) {
		t.Fatalf("LsRemote tag returned non-SHA: %q", sha)
	}
	if sha != r.TagSHA {
		t.Fatalf("LsRemote tag SHA = %q (likely the tag object), want commit SHA %q", sha, r.TagSHA)
	}
}

func TestLsRemote_RefNotFound(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)

	_, err := LsRemote(testCtx(t), r.Path, "refs/heads/nonexistent-branch")
	if err == nil {
		t.Fatal("expected error for missing ref")
	}
	if !errors.Is(err, ErrRefNotFound) && !errors.Is(err, ErrShellFailed) {
		t.Fatalf("expected ErrRefNotFound or ErrShellFailed, got %v", err)
	}
}

func TestLsRemote_InlineCredentialHTTPSWithPassword(t *testing.T) {
	withDetectReset(t)

	_, err := LsRemote(testCtx(t), "https://user:pass@example.com/repo.git", "main")
	if !errors.Is(err, ErrInlineCredential) {
		t.Fatalf("expected ErrInlineCredential, got %v", err)
	}
}

func TestLsRemote_InlineCredentialHTTPSPAT(t *testing.T) {
	withDetectReset(t)

	// Common shape for a GitHub personal-access-token-in-URL.
	_, err := LsRemote(testCtx(t), "https://ghp_xxxx@github.com/o/r.git", "main")
	if !errors.Is(err, ErrInlineCredential) {
		t.Fatalf("expected ErrInlineCredential, got %v", err)
	}
}

func TestLsRemote_SSHGitUser_NotCredential(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	// `git@host:path` is scp-style; checkInlineCredential permits it
	// because scp-style has no password component. The ls-remote call
	// will fail (no such host) but the error must be from the network,
	// not the credential check.
	_, err := LsRemote(testCtx(t), "git@host.invalid:owner/repo.git", "main")
	if errors.Is(err, ErrInlineCredential) {
		t.Fatalf("scp-style URL incorrectly flagged as credentialed: %v", err)
	}
}

func TestLsRemote_EmptyRef(t *testing.T) {
	withDetectReset(t)

	_, err := LsRemote(testCtx(t), "https://example.com/r.git", "")
	if err == nil {
		t.Fatal("expected error for empty ref")
	}
	if errors.Is(err, ErrRefNotFound) {
		t.Fatalf("empty ref should not surface as ErrRefNotFound: %v", err)
	}
}

func TestClone_Happy(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")

	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "HEAD")); err != nil {
		t.Fatalf("bare clone missing HEAD: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "objects")); err != nil {
		t.Fatalf("bare clone missing objects/: %v", err)
	}
}

func TestClone_RelativeDestRejected(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	err := Clone(testCtx(t), "https://example.com/r.git", "relative/path")
	if err == nil {
		t.Fatal("expected error for relative destination")
	}
}

func TestClone_InlineCredentialRejectedBeforeNetwork(t *testing.T) {
	withDetectReset(t)

	err := Clone(testCtx(t), "https://u:p@example.com/r.git", filepath.Join(t.TempDir(), "out"))
	if !errors.Is(err, ErrInlineCredential) {
		t.Fatalf("expected ErrInlineCredential, got %v", err)
	}
}

func TestFetch_Happy(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// Make a new commit on the remote.
	write(t, filepath.Join(r.Path, "AGENTS.md"), "third\n")
	mustGit(t, r.Path, "add", "AGENTS.md")
	mustGit(t, r.Path, "commit", "--quiet", "-m", "third")
	newSHA := mustGit(t, r.Path, "rev-parse", "HEAD")

	// Point the bare clone at the updated remote and fetch.
	mustGit(t, dst, "remote", "set-url", "origin", r.Path)
	if err := Fetch(testCtx(t), dst); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The new SHA must now resolve locally.
	have := mustGit(t, dst, "rev-parse", newSHA)
	if have != newSHA {
		t.Fatalf("after fetch, local rev-parse %s = %q", newSHA, have)
	}
}

func TestIsAncestor_True(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	ok, err := IsAncestor(testCtx(t), dst, r.InitialSHA, r.SecondSHA)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Fatalf("expected initial SHA to be ancestor of second SHA")
	}
}

func TestIsAncestor_False(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	ok, err := IsAncestor(testCtx(t), dst, r.SecondSHA, r.InitialSHA)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if ok {
		t.Fatalf("expected second SHA NOT to be ancestor of initial SHA")
	}
}

func TestHasRef_BranchExists(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	ok, err := HasRef(testCtx(t), dst, "refs/heads/"+r.HeadBranch)
	if err != nil {
		t.Fatalf("HasRef: %v", err)
	}
	if !ok {
		t.Fatalf("expected refs/heads/%s to exist after clone", r.HeadBranch)
	}
}

func TestHasRef_TagExists(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	ok, err := HasRef(testCtx(t), dst, "refs/tags/"+r.TagName)
	if err != nil {
		t.Fatalf("HasRef: %v", err)
	}
	if !ok {
		t.Fatalf("expected refs/tags/%s to exist after clone (Clone fetches tags)", r.TagName)
	}
}

func TestHasRef_Missing(t *testing.T) {
	requireGit(t)
	withDetectReset(t)

	r := makeRepo(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(testCtx(t), r.Path, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	ok, err := HasRef(testCtx(t), dst, "refs/heads/does-not-exist")
	if err != nil {
		t.Fatalf("HasRef: %v", err)
	}
	if ok {
		t.Fatal("expected refs/heads/does-not-exist to be absent")
	}
}

func TestHasRef_RelativeRepoPathRejected(t *testing.T) {
	withDetectReset(t)

	_, err := HasRef(testCtx(t), "relative/path", "refs/heads/main")
	if err == nil {
		t.Fatal("expected error for relative repo path")
	}
}

func TestHasRef_EmptyRef(t *testing.T) {
	withDetectReset(t)

	_, err := HasRef(testCtx(t), filepath.Join(t.TempDir(), "x"), "")
	if err == nil {
		t.Fatal("expected error for empty ref")
	}
}

func TestFirstShaFromLsRemote(t *testing.T) {
	const (
		tagObjectSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		commitSHA    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		mainSHA      = "1234567890abcdef1234567890abcdef12345678"
	)
	cases := []struct {
		name    string
		in      string
		wantSHA string
	}{
		{"simple", mainSHA + "\trefs/heads/main\n", mainSHA},
		{"skip_short_line", "short\n" + mainSHA + "\trefs/heads/main\n", mainSHA},
		{"empty", "", ""},
		{"uppercase_normalized", strings.ToUpper(mainSHA) + "\trefs/heads/main\n", mainSHA},
		// Annotated tag: two lines, the second is the dereferenced
		// commit SHA suffixed with `^{}`. The peel must win so
		// downstream callers receive the commit SHA, not the tag
		// object SHA.
		{
			"annotated_tag_prefers_peel",
			tagObjectSHA + "\trefs/tags/v1\n" + commitSHA + "\trefs/tags/v1^{}\n",
			commitSHA,
		},
		// Peel-after-trailing-newline: the peeled line is last and
		// followed by a trailing newline. Same expectation.
		{
			"annotated_tag_with_trailing_newline",
			tagObjectSHA + "\trefs/tags/v1\n" + commitSHA + "\trefs/tags/v1^{}\n\n",
			commitSHA,
		},
		// Lightweight tag / branch: a single line with no peel.
		// Falls back to the first matched SHA.
		{
			"lightweight_tag_uses_first_sha",
			commitSHA + "\trefs/tags/v1\n",
			commitSHA,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstShaFromLsRemote([]byte(tc.in))
			if got != tc.wantSHA {
				t.Fatalf("firstShaFromLsRemote(%q) = %q, want %q", tc.in, got, tc.wantSHA)
			}
		})
	}
}
