// Package git materializes and reads canonical-source repositories for
// aienvs.
//
// The package splits its two concerns by transport path:
//
//   - shell.go / resolve.go / materialize.go — network-facing operations
//     shell out to the installed `git` binary. This matches the pattern
//     used by Go's own `cmd/go`: authentication, credential handling, and
//     transport concerns are delegated to a single well-tested tool.
//
//   - read.go — read-only tree/blob access uses go-git v5 against the
//     materialized cache directory. No network calls ever go through
//     go-git.
//
// Callers MUST invoke [DetectGit] once at startup before any other
// shell-out entry point; the shell-outs do not re-detect on every call.
// All shell-outs take a [context.Context] first argument; cancellation is
// honored by [exec.CommandContext].
//
// Credential handling is paranoid by design: every shell-out sets
// GIT_TERMINAL_PROMPT=0, GIT_ASKPASS=, and SSH_ASKPASS= to guarantee the
// child process never prompts interactively, and URLs containing inline
// credentials are rejected with [ErrInlineCredential] before they reach
// `git`.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Sentinel errors. Callers expected to branch on any of these should use
// errors.Is; wrapping with %w preserves the sentinel through message
// enrichment.
var (
	// ErrGitNotFound is returned when `git` is not available on PATH.
	// [DetectGit] sets this; the network-facing shell-outs also surface it
	// if invoked before detection succeeded.
	ErrGitNotFound = errors.New("git: `git` binary not found on PATH")

	// ErrInlineCredential is returned when a URL contains a password in
	// its userinfo component. Canonicalization strips HTTPS credentials
	// as part of normalization; this sentinel is the defense-in-depth
	// layer that rejects credentialed URLs before they are passed to
	// `git`.
	ErrInlineCredential = errors.New("git: URL contains inline credentials")

	// ErrRemoteUnreachable is returned when a network operation failed
	// in a way that suggests the remote could not be reached (DNS, TCP,
	// TLS). The underlying `git` exit is wrapped with %w.
	ErrRemoteUnreachable = errors.New("git: remote unreachable")

	// ErrRefNotFound is returned when ls-remote does not return a SHA
	// for the requested ref.
	ErrRefNotFound = errors.New("git: ref not found")

	// ErrShellFailed is a generic wrapper for non-specific git failures
	// so callers can attach structured context while still exposing the
	// underlying exec error via errors.Unwrap.
	ErrShellFailed = errors.New("git: shell command failed")
)

// shaPattern matches a full 40-char lowercase hex SHA-1.
var shaPattern = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

// detectState caches the result of DetectGit. A detected path is stored
// so tests and callers can inject an alternate `git` via GIT_EXECUTABLE
// without re-running PATH lookup on every shell-out.
var (
	detectOnce sync.Once
	detectErr  error
	gitPath    string
)

// DetectGit confirms that a `git` executable is available and caches its
// path for subsequent shell-outs. It is safe to call multiple times and
// from multiple goroutines; the underlying lookup runs exactly once per
// process.
//
// Returning [ErrGitNotFound] is the only failure mode. Callers at CLI
// entry points should surface the remediation text documented on
// [ErrGitNotFound] rather than propagating the raw error.
func DetectGit() error {
	detectOnce.Do(func() {
		if override := strings.TrimSpace(os.Getenv("AIENVS_GIT_EXECUTABLE")); override != "" {
			// gosec G304/G703: the override is an explicit opt-in env var
			// for CI hosts that need to pin `git` to a known location.
			// The stat is only used to produce an early, clear error; the
			// path is not opened or executed here.
			if _, err := os.Stat(override); err != nil { //nolint:gosec
				detectErr = fmt.Errorf("%w: AIENVS_GIT_EXECUTABLE=%q: %w", ErrGitNotFound, override, err)
				return
			}
			gitPath = override
			return
		}
		p, err := exec.LookPath("git")
		if err != nil {
			detectErr = fmt.Errorf("%w: %w", ErrGitNotFound, err)
			return
		}
		gitPath = p
	})
	return detectErr
}

// GitPath returns the resolved path to the `git` binary, or the empty
// string if [DetectGit] has not succeeded. Exposed for test assertions
// and diagnostic logging only.
func GitPath() string {
	return gitPath
}

// resetDetectForTests allows the test binary to re-run detection with a
// different AIENVS_GIT_EXECUTABLE. It is intentionally unexported.
func resetDetectForTests() {
	detectOnce = sync.Once{}
	detectErr = nil
	gitPath = ""
}

// checkInlineCredential rejects URLs whose userinfo component contains a
// password. scp-style (`git@host:path`) URLs have no userinfo syntax that
// admits a password, so they are accepted here; the canonical form is
// also checked upstream.
func checkInlineCredential(raw string) error {
	// scp-style never carries a password component.
	if !strings.Contains(raw, "://") {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		// nilerr: intentional. This guard is narrowly scoped to inline
		// credentials. An unparseable URL is a different failure class;
		// downstream (`git` itself) produces the canonical error message
		// with more transport context than we could here. Returning nil
		// avoids double-reporting the same problem with two different
		// error surfaces.
		return nil //nolint:nilerr
	}
	if u.User == nil {
		return nil
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return fmt.Errorf("%w: %q", ErrInlineCredential, u.Scheme+"://"+u.Host+u.Path)
	}
	// HTTPS URLs that carry only a username but no password are also
	// rejected: GitHub personal-access-token embedded URLs take this
	// shape (`https://<pat>@github.com/...`). SSH URLs legitimately use
	// the `git@` username.
	scheme := strings.ToLower(u.Scheme)
	if scheme == "https" || scheme == "http" {
		if u.User.Username() != "" {
			return fmt.Errorf("%w: %q", ErrInlineCredential, u.Scheme+"://"+u.Host+u.Path)
		}
	}
	return nil
}

// gitCmd constructs an *exec.Cmd with environment hardening applied:
// no interactive prompts, no credential helpers, and no pager. Callers
// supply the remaining args in the order `git` expects them.
//
// dir, if non-empty, is passed through to exec.Cmd.Dir so the command
// runs inside a specific repository directory. Callers invoking
// `ls-remote` or other network-only commands pass dir="".
func gitCmd(ctx context.Context, dir string, args ...string) (*exec.Cmd, error) {
	if err := DetectGit(); err != nil {
		return nil, err
	}
	// gosec G204: `git` is the only executable ever launched; its path is
	// either the resolved `exec.LookPath("git")` or a deliberate operator
	// override. Args are fixed verbs + caller-supplied values we already
	// guarded (checkInlineCredential / absolute-path / SHA regex) upstream.
	cmd := exec.CommandContext(ctx, gitPath, args...) //nolint:gosec
	cmd.Dir = dir
	// Start from a hardened env: inherit PATH/HOME/etc. but force the
	// prompt-disabling knobs on regardless of what the parent set.
	env := os.Environ()
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		// GCM_INTERACTIVE=never neutralizes Git Credential Manager's
		// popup on Windows; harmless elsewhere.
		"GCM_INTERACTIVE=never",
		// Disable the pager so long outputs don't block on a TTY.
		"GIT_PAGER=cat",
		// Deterministic locale for machine-readable output.
		"LC_ALL=C",
	)
	cmd.Env = env
	return cmd, nil
}

// runCapture executes cmd and returns (stdout, stderr, error). On
// non-zero exit the error is [ErrShellFailed] wrapping the underlying
// exec error, with stderr attached to the message for diagnostics.
func runCapture(cmd *exec.Cmd, label string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("%w: %s: %w: %s", ErrShellFailed, label, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// LsRemote returns the 40-char lowercase-hex SHA that `ref` resolves to
// on `url`. `ref` may be a branch or tag; literal SHAs are short-
// circuited by [ResolveRef] before reaching this entry point.
//
// The underlying command is
// `git ls-remote --exit-code -- <url> <ref> <ref>^{}`. The `^{}` peel
// pattern is included so that annotated tags return both the tag-object
// SHA and the dereferenced commit SHA, letting [firstShaFromLsRemote]
// pick the commit. For branches and lightweight tags the peel pattern
// matches nothing and is silently ignored. --exit-code still ensures
// that a ref returning no matches becomes a clean [ErrRefNotFound]
// rather than a silent empty output.
func LsRemote(ctx context.Context, rawURL, ref string) (string, error) {
	if err := checkInlineCredential(rawURL); err != nil {
		return "", err
	}
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("git: ls-remote: empty ref")
	}

	cmd, err := gitCmd(ctx, "", "ls-remote", "--exit-code", "--", rawURL, ref, ref+"^{}")
	if err != nil {
		return "", err
	}
	stdout, stderr, err := runCapture(cmd, "ls-remote")
	if err != nil {
		// `git ls-remote --exit-code` exits with 2 when no refs matched.
		if exitErr := (&exec.ExitError{}); errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return "", fmt.Errorf("%w: %q on %q", ErrRefNotFound, ref, rawURL)
		}
		if looksLikeNetworkFailure(stderr) {
			return "", fmt.Errorf("%w: %w", ErrRemoteUnreachable, err)
		}
		return "", err
	}

	// ls-remote output is `<sha>\t<ref>` per line. Multiple lines mean
	// the ref matched both `refs/heads/<x>` and `refs/tags/<x>`; we take
	// the first line that is an exact ref match if provided as a glob,
	// otherwise the first line overall (consistent with git's own tie-
	// breaker for non-glob refs).
	sha := firstShaFromLsRemote(stdout)
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("%w: ls-remote produced unexpected output for %q: %q", ErrShellFailed, ref, strings.TrimSpace(string(stdout)))
	}
	return sha, nil
}

// firstShaFromLsRemote extracts the SHA from `git ls-remote` output.
// Lines that do not begin with a 40-char SHA are skipped to tolerate
// warnings printed to stdout on some git builds.
//
// Annotated-tag handling: `git ls-remote` reports an annotated tag on
// two lines — first the tag *object* SHA (which is not a commit), then
// the dereferenced commit SHA suffixed with `^{}`. Downstream
// [Repository.ReadTree] resolves SHAs through `CommitObject`, which
// fails for tag-object SHAs, so when a peeled (`^{}`) line is present
// we prefer it. For lightweight tags, branches, and SHA queries (single
// line, no peel) we fall back to the first matched SHA.
func firstShaFromLsRemote(stdout []byte) string {
	var firstSHA string
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 40 {
			continue
		}
		candidate := strings.ToLower(line[:40])
		if !shaPattern.MatchString(candidate) {
			continue
		}
		if strings.HasSuffix(line, "^{}") {
			return candidate
		}
		if firstSHA == "" {
			firstSHA = candidate
		}
	}
	return firstSHA
}

// networkFailurePatterns is a small allowlist of substrings that strongly
// indicate a transport-level failure. The list is intentionally narrow:
// we prefer to mis-classify a rare network failure as ErrShellFailed than
// mask a credential or policy failure behind ErrRemoteUnreachable.
var networkFailurePatterns = []string{
	"Could not resolve host",
	"could not resolve host",
	"Temporary failure in name resolution",
	"Connection refused",
	"connection refused",
	"Network is unreachable",
	"network is unreachable",
	"No route to host",
	"no route to host",
	"Operation timed out",
	"Connection timed out",
	"connection timed out",
	"SSL_ERROR",
	"unable to access",
}

func looksLikeNetworkFailure(stderr []byte) bool {
	s := string(stderr)
	for _, pat := range networkFailurePatterns {
		if strings.Contains(s, pat) {
			return true
		}
	}
	return false
}

// Clone performs a bare clone of `url` into `dst`, which must be an
// empty or non-existent directory. The clone is bare (no working tree)
// because aienvs reads repository contents through [ReadTree] /
// [BlobContent] rather than via a checkout.
//
// Parent directories of dst are created with 0o750 if missing; dst
// itself must not already exist as a populated directory (git refuses to
// clone into a non-empty target).
func Clone(ctx context.Context, rawURL, dst string) error {
	if err := checkInlineCredential(rawURL); err != nil {
		return err
	}
	if !filepath.IsAbs(dst) {
		return fmt.Errorf("git: clone: destination must be absolute, got %q", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("git: clone: mkdir parent of %q: %w", dst, err)
	}

	// Tags are fetched on the initial clone (default git behavior — no
	// `--no-tags`) so a tag-pinned manifest can be materialized on a
	// fresh cache. This matches [Fetch], which uses `--tags` to keep
	// the mirror's tag namespace current on subsequent syncs.
	cmd, err := gitCmd(ctx, "", "clone", "--bare", "--quiet", "--", rawURL, dst)
	if err != nil {
		return err
	}
	_, stderr, err := runCapture(cmd, "clone")
	if err != nil {
		if looksLikeNetworkFailure(stderr) {
			return fmt.Errorf("%w: %w", ErrRemoteUnreachable, err)
		}
		return err
	}
	return nil
}

// Fetch updates the bare clone at `repoPath` from its origin remote.
// It fetches all branches and tags so a subsequent [Reachable] check has
// the data it needs.
func Fetch(ctx context.Context, repoPath string) error {
	if !filepath.IsAbs(repoPath) {
		return fmt.Errorf("git: fetch: repo path must be absolute, got %q", repoPath)
	}
	cmd, err := gitCmd(ctx, repoPath,
		"fetch", "--quiet", "--prune", "--tags",
		"origin",
		"+refs/heads/*:refs/heads/*",
	)
	if err != nil {
		return err
	}
	_, stderr, err := runCapture(cmd, "fetch")
	if err != nil {
		if looksLikeNetworkFailure(stderr) {
			return fmt.Errorf("%w: %w", ErrRemoteUnreachable, err)
		}
		return err
	}
	return nil
}

// IsAncestor returns true when `ancestor` is an ancestor of `descendant`
// in the repository at repoPath. Both arguments are resolved as refs or
// SHAs. This is the reachability check used to defend against
// force-pushed refs: after fetching, callers verify the pinned SHA is
// reachable from the configured ref.
//
// A clean "false" answer is returned on exit code 1; any other exit is
// wrapped as [ErrShellFailed].
func IsAncestor(ctx context.Context, repoPath, ancestor, descendant string) (bool, error) {
	if !filepath.IsAbs(repoPath) {
		return false, fmt.Errorf("git: is-ancestor: repo path must be absolute, got %q", repoPath)
	}
	cmd, err := gitCmd(ctx, repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case 1:
			return false, nil
		}
	}
	return false, fmt.Errorf("%w: is-ancestor %s..%s: %w: %s", ErrShellFailed, ancestor, descendant, err, strings.TrimSpace(stderr.String()))
}
