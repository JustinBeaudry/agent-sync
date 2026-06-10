// Package gittest provides shared git test helpers used by the test suites
// of multiple internal packages. It lives in a normal (non-_test) file so it
// can be imported across package boundaries; because only _test.go files
// reference it, it never links into the production binary.
package gittest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// RequireGit skips the test when git is unavailable on PATH. When
// AGENT_SYNC_REQUIRE_GIT is set (CI), a missing git is a hard failure instead
// of a skip, so git-backed tests can never silently vanish from a CI run.
func RequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("AGENT_SYNC_REQUIRE_GIT") != "" {
			t.Fatalf("git not available on PATH but AGENT_SYNC_REQUIRE_GIT is set: %v", err)
		}
		t.Skipf("git not available on PATH: %v", err)
	}
}

// MustGit runs `git <args...>` in dir with a hermetic environment (no global or
// system config, no credential/terminal prompts, deterministic identity and
// locale) and returns trimmed combined output. It fails the test on error.
func MustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// #nosec G204 -- test helper: the binary is the fixed string "git" and
	// args are test-authored subcommands, never external/untrusted input.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=agent-sync-test",
		"GIT_AUTHOR_EMAIL=test@agent-sync.invalid",
		"GIT_COMMITTER_NAME=agent-sync-test",
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
