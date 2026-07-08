package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"

	"github.com/agent-sync/agent-sync/internal/cache"
	"github.com/agent-sync/agent-sync/internal/gittest"
	"github.com/agent-sync/agent-sync/internal/trust"
)

// requireGit / mustGit delegate to the shared gittest helpers; kept as
// package-local aliases so this package's many call sites stay unchanged.
func requireGit(t *testing.T) {
	t.Helper()
	gittest.RequireGit(t)
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.MustGit(t, dir, args...)
}

// makeCanonicalRepo builds a local git repo with one rule file and returns
// its path + HEAD sha.
func makeCanonicalRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch=main", "--quiet")
	ruleDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ruleDir, "no-fri.md"), []byte("No PRs on Friday.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "--quiet", "-m", "canonical")
	return dir, mustGit(t, dir, "rev-parse", "HEAD")
}

// writeWorkspace creates a workspace dir with a pinned local_path manifest
// targeting claude.
func writeWorkspace(t *testing.T, canonicalPath, sha string) string {
	t.Helper()
	ws := t.TempDir()
	manifest := "version: 1\n" +
		"canonical:\n" +
		"  local_path: " + canonicalPath + "\n" +
		"  commit: " + sha + "\n" +
		"trusted_sha: " + sha + "\n" +
		"targets:\n" +
		"  - claude\n"
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func runSync(t *testing.T, ws string, extraArgs ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	args := append([]string{"sync", "--workspace", ws, "--non-interactive"}, extraArgs...)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

type servedURLRepo struct {
	Worktree string
	Bare     string
	URL      string
	process  *os.Process
	stderr   *bytes.Buffer
}

func setTestXDG(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	return base
}

func writeURLWorkspace(t *testing.T, canonicalURL, sha, ref string, auto *bool) string {
	t.Helper()
	ws := t.TempDir()
	var b strings.Builder
	b.WriteString("version: 1\ncanonical:\n")
	b.WriteString("  url: " + canonicalURL + "\n")
	if ref != "" {
		b.WriteString("  ref: " + ref + "\n")
	}
	b.WriteString("  commit: " + sha + "\n")
	if auto != nil {
		if *auto {
			b.WriteString("  auto: true\n")
		} else {
			b.WriteString("  auto: false\n")
		}
	}
	b.WriteString("trusted_sha: " + sha + "\n")
	b.WriteString("targets:\n  - claude\n")
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func serveCanonicalRepo(t *testing.T, worktree string) *servedURLRepo {
	t.Helper()
	// `git daemon` (git:// transport) is not reliably available on Git for
	// Windows — it exits non-zero under the CI runner. URL-source auto-advance
	// behavior is exercised on Linux/macOS here and, transport-independently, by
	// the local_path auto-advance tests on every platform.
	if runtime.GOOS == "windows" {
		t.Skip("git daemon (git:// transport) is unavailable on Windows; covered on unix + via local_path tests")
	}
	base := t.TempDir()
	bare := filepath.Join(base, "origin.git")
	mustGit(t, base, "clone", "--bare", "--quiet", worktree, bare)

	port := freePort(t)
	var stderr bytes.Buffer
	pidFile := filepath.Join(base, "git-daemon.pid")
	cmd := exec.Command("git", "daemon",
		"--reuseaddr",
		"--export-all",
		"--detach",
		"--pid-file="+pidFile,
		"--base-path="+base,
		"--listen=127.0.0.1",
		fmt.Sprintf("--port=%d", port),
		base,
	)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("start git daemon: %v", err)
	}
	// `git daemon --detach` returns from Run() before the detached process has
	// written its --pid-file (the write is async in the child). Poll for it
	// rather than reading immediately, which races and loses under slower /
	// instrumented CI runs. Give it up to ~4s.
	var pidBytes []byte
	var err error
	for range 200 {
		pidBytes, err = os.ReadFile(pidFile)
		if err == nil && len(strings.TrimSpace(string(pidBytes))) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || len(strings.TrimSpace(string(pidBytes))) == 0 {
		t.Fatalf("read git daemon pid: %v (stderr: %s)", err, stderr.String())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse git daemon pid %q: %v", strings.TrimSpace(string(pidBytes)), err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find git daemon process: %v", err)
	}
	srv := &servedURLRepo{
		Worktree: worktree,
		Bare:     bare,
		URL:      fmt.Sprintf("git://127.0.0.1:%d/%s", port, filepath.Base(bare)),
		process:  proc,
		stderr:   &stderr,
	}
	waitForGitDaemon(t, srv.URL, &stderr)
	t.Cleanup(func() { srv.Stop() })
	return srv
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForGitDaemon(t *testing.T, url string, stderr *bytes.Buffer) {
	t.Helper()
	var lastErr error
	for range 50 {
		// #nosec G204 -- test helper: binary is fixed and args are test-authored.
		cmd := exec.Command("git", "ls-remote", url, "HEAD")
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_TERMINAL_PROMPT=0",
			"LC_ALL=C",
		)
		if out, err := cmd.CombinedOutput(); err == nil {
			return
		} else {
			lastErr = fmt.Errorf("%w: %s", err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("git daemon at %s did not become ready: %v\nstderr: %s", url, lastErr, stderr.String())
}

func waitForGitDaemonDown(t *testing.T, url string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		// #nosec G204 -- test helper: binary is fixed and args are test-authored.
		cmd := exec.Command("git", "ls-remote", url, "HEAD")
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_TERMINAL_PROMPT=0",
			"LC_ALL=C",
		)
		if err := cmd.Run(); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("git daemon at %s stayed reachable after Stop()", url)
}

func (s *servedURLRepo) PushMain(t *testing.T) {
	t.Helper()
	mustGit(t, s.Worktree, "push", "--force", s.Bare, "main:main")
}

func (s *servedURLRepo) Stop() {
	if s == nil || s.process == nil {
		return
	}
	_ = s.process.Kill()
	s.process = nil
}

func auditContentsForURL(t *testing.T, url string) string {
	t.Helper()
	canonical, err := cache.Canonicalize(url)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	loc, err := cache.Resolve(canonical, cache.ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve cache: %v", err)
	}
	data, err := os.ReadFile(loc.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	return string(data)
}

func autoAdvanceLogCount(stderr string) int {
	return strings.Count(stderr, `"msg":"sync auto-advance applied"`)
}

func revokeTrustURL(t *testing.T, url string, now time.Time) {
	t.Helper()
	canonical, err := cache.Canonicalize(url)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	path, err := xdg.DataFile(filepath.Join("agent-sync", "trust.jsonl"))
	if err != nil {
		t.Fatalf("trust path: %v", err)
	}
	store := trust.NewStore(path)
	if err := store.Append(trust.LogEntry{
		TS:       now,
		TSRaw:    now.UTC().Format(time.RFC3339),
		Op:       trust.OpRevoke,
		URL:      canonical,
		PrevSHA:  "1111111111111111111111111111111111111111",
		Source:   trust.SourceCLI,
		Actor:    "test",
		Hostname: "testhost",
	}); err != nil {
		t.Fatalf("append revoke: %v", err)
	}
}

func TestSync_LocalPathEndToEnd(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	out, errOut, err := runSync(t, ws)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	// The rule file landed in the workspace.
	ruleFile := filepath.Join(ws, ".claude", "rules", "agent-sync", "no-fri.md")
	if _, statErr := os.Stat(ruleFile); statErr != nil {
		t.Fatalf("expected rule file %s: %v", ruleFile, statErr)
	}
	// The ledger was written.
	if _, statErr := os.Stat(filepath.Join(ws, ".agent-sync", "state", "claude.json")); statErr != nil {
		t.Fatalf("expected ledger: %v", statErr)
	}
	_ = out
}

func TestSync_JSONOutput(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	out, errOut, err := runSync(t, ws, "--output", "json")
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		Commit        string `json:"commit"`
		Summary       struct {
			ExitCode int `json:"exit_code"`
		} `json:"summary"`
	}
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if doc.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	if doc.Commit != sha {
		t.Fatalf("commit = %q, want %q", doc.Commit, sha)
	}
	if doc.Summary.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", doc.Summary.ExitCode)
	}
}

func TestSync_SecondRunUnchanged(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	ws := writeWorkspace(t, canonical, sha)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("first sync: %v\n%s", err, errOut)
	}
	out, errOut, err := runSync(t, ws, "--output", "json")
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "\"unchanged\"") {
		t.Fatalf("second sync should report an unchanged target:\n%s", out)
	}
}

func TestSync_FloatingLocalPathUnsupported(t *testing.T) {
	requireGit(t)
	canonical, _ := makeCanonicalRepo(t)
	ws := t.TempDir()
	// Manifest with local_path but no commit → floating, unsupported.
	m := "version: 1\ncanonical:\n  local_path: " + canonical + "\ntargets:\n  - claude\n"
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runSync(t, ws)
	if err == nil {
		t.Fatal("expected error for floating local_path")
	}
}

// TestPromptYes pins the [Y/n] semantics: Enter and y are consent, n and an
// unanswerable (closed) stdin are not — never write home on a read error.
func TestPromptYes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"enter defaults yes", "\n", true},
		{"y", "y\n", true},
		{"yes", "yes\n", true},
		{"n", "n\n", false},
		{"no", "No\n", false},
		{"eof without input", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errBuf bytes.Buffer
			got := promptYes(strings.NewReader(tc.in), &errBuf, "sync user? [Y/n] ")
			if got != tc.want {
				t.Fatalf("promptYes(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if !strings.Contains(errBuf.String(), "[Y/n]") {
				t.Fatalf("prompt not written: %q", errBuf.String())
			}
		})
	}
}

// TestSync_NoAdapterStartedBannerOnStderr pins plan R18: bundled in-process
// adapters must not spray per-session "<name>: started" banners onto the
// CLI's stderr (the adapterkit banner is subprocess proof-of-life for the
// stderr ring; in-process it is duplicate noise printed once per session).
// The banner bypasses the cobra writers, so capture the real os.Stderr.
func TestSync_NoAdapterStartedBannerOnStderr(t *testing.T) {
	ws := writeLocalDirWorkspace(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = w
	_, _, syncErr := runSync(t, ws, "--offline")
	os.Stderr = oldStderr
	_ = w.Close()
	captured, _ := io.ReadAll(r)
	_ = r.Close()

	if syncErr != nil {
		t.Fatalf("sync failed: %v", syncErr)
	}
	if strings.Contains(string(captured), "started") {
		t.Fatalf("bundled adapter session banner leaked to stderr:\n%s", captured)
	}
}

func TestSync_LocalPathAutoAdvanceEndToEnd(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	// local_path source: no git daemon, offline by nature. Exercises the
	// local branch of prepareAutoAdvanceScope + resolveLocalAdvanceTarget.
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	newSHA := commitFile(t, canonical, "rules/local-new.md", "Local new.\n", "second local rule")

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("auto-advance sync: %v\nstderr: %s", err, errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+newSHA) {
		t.Fatalf("local_path commit not re-pinned to %s:\n%s", newSHA, man)
	}
	if !strings.Contains(man, "trusted_sha: "+newSHA) {
		t.Fatalf("local_path trusted_sha not re-pinned to %s:\n%s", newSHA, man)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "local-new.md")); statErr != nil {
		t.Fatalf("expected synced rule from auto-advanced local_path commit: %v", statErr)
	}
}

func TestSync_LocalPathFrozenSkipsAutoAdvance(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}
	_ = commitFile(t, canonical, "rules/local-new.md", "Local new.\n", "second local rule")

	if _, errOut, err := runSync(t, ws, "--frozen"); err != nil {
		t.Fatalf("frozen sync: %v\nstderr: %s", err, errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+head) {
		t.Fatalf("--frozen must keep the original pin %s:\n%s", head, man)
	}
}

func TestSync_URLAutoAdvanceEndToEnd(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	worktree, _, head := makeUpdateRepo(t)
	srv := serveCanonicalRepo(t, worktree)
	ws := writeURLWorkspace(t, srv.URL, head, "main", nil)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	newSHA := commitFile(t, worktree, "rules/more.md", "More rules.\n", "second rule")
	srv.PushMain(t)

	_, errOut, err := runSync(t, ws)
	if err != nil {
		t.Fatalf("auto-advance sync: %v\nstderr: %s", err, errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+newSHA) {
		t.Fatalf("manifest commit not re-pinned to %s:\n%s", newSHA, man)
	}
	if !strings.Contains(man, "trusted_sha: "+newSHA) {
		t.Fatalf("manifest trusted_sha not re-pinned to %s:\n%s", newSHA, man)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "more.md")); statErr != nil {
		t.Fatalf("expected synced rule from auto-advanced commit: %v", statErr)
	}
	if got := autoAdvanceLogCount(errOut); got != 1 {
		t.Fatalf("auto-advance log count = %d, want 1\nstderr: %s", got, errOut)
	}
	audit := auditContentsForURL(t, srv.URL)
	if got := strings.Count(audit, "auto-advance "); got != 1 {
		t.Fatalf("auto-advance audit entry count = %d, want 1\naudit:\n%s", got, audit)
	}
	if !strings.Contains(audit, "old_sha="+head) || !strings.Contains(audit, "new_sha="+newSHA) {
		t.Fatalf("audit missing old/new SHAs:\n%s", audit)
	}
}

func TestSync_URLFrozenSkipsAutoAdvance(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	worktree, _, head := makeUpdateRepo(t)
	srv := serveCanonicalRepo(t, worktree)
	ws := writeURLWorkspace(t, srv.URL, head, "main", nil)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	_ = commitFile(t, worktree, "rules/more.md", "More rules.\n", "second rule")
	srv.PushMain(t)

	if _, errOut, err := runSync(t, ws, "--frozen"); err != nil {
		t.Fatalf("sync --frozen: %v\nstderr: %s", err, errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+head) || !strings.Contains(man, "trusted_sha: "+head) {
		t.Fatalf("frozen sync should keep pinned manifest:\n%s", man)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "more.md")); !os.IsNotExist(statErr) {
		t.Fatalf("frozen sync should not land the newer rule, stat err = %v", statErr)
	}
	audit := auditContentsForURL(t, srv.URL)
	if strings.Contains(audit, "auto-advance ") {
		t.Fatalf("frozen sync should not append audit entries:\n%s", audit)
	}
}

func TestSync_URLManifestAutoFalseSkipsAdvance(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	worktree, _, head := makeUpdateRepo(t)
	srv := serveCanonicalRepo(t, worktree)
	auto := false
	ws := writeURLWorkspace(t, srv.URL, head, "main", &auto)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	_ = commitFile(t, worktree, "rules/more.md", "More rules.\n", "second rule")
	srv.PushMain(t)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("sync auto:false: %v\nstderr: %s", err, errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+head) || !strings.Contains(man, "trusted_sha: "+head) {
		t.Fatalf("auto:false sync should keep pinned manifest:\n%s", man)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "more.md")); !os.IsNotExist(statErr) {
		t.Fatalf("auto:false sync should not land the newer rule, stat err = %v", statErr)
	}
}

func TestSync_URLRevokedTrustBlocksAutoAdvance(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	worktree, _, head := makeUpdateRepo(t)
	srv := serveCanonicalRepo(t, worktree)
	ws := writeURLWorkspace(t, srv.URL, head, "main", nil)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	_ = commitFile(t, worktree, "rules/more.md", "More rules.\n", "second rule")
	srv.PushMain(t)
	revokeTrustURL(t, srv.URL, now)

	_, errOut, err := runSync(t, ws)
	if err == nil {
		t.Fatal("expected revoked trust anchor to block auto-advance")
	}
	if code := MapExit(err); code != trust.ExitRevokedTrustAnchor {
		t.Fatalf("exit code = %d, want %d", code, trust.ExitRevokedTrustAnchor)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+head) || !strings.Contains(man, "trusted_sha: "+head) {
		t.Fatalf("revoked trust should leave the manifest pinned:\n%s", man)
	}
	if got := autoAdvanceLogCount(errOut); got != 0 {
		t.Fatalf("revoked trust should not log an applied advance, got %d\nstderr: %s", got, errOut)
	}
}

func TestSync_OfflineFallbackUsesCachedPinWithoutAdvanceRecord(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	worktree, _, head := makeUpdateRepo(t)
	srv := serveCanonicalRepo(t, worktree)
	ws := writeURLWorkspace(t, srv.URL, head, "main", nil)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	_ = commitFile(t, worktree, "rules/more.md", "More rules.\n", "second rule")
	srv.PushMain(t)
	srv.Stop()
	waitForGitDaemonDown(t, srv.URL)

	_, errOut, err := runSync(t, ws)
	if err != nil {
		t.Fatalf("offline-fallback sync: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(errOut, "fell back to the cached pin") {
		t.Fatalf("expected offline fallback warning, stderr:\n%s", errOut)
	}
	man := string(readManifest(t, ws))
	if !strings.Contains(man, "commit: "+head) || !strings.Contains(man, "trusted_sha: "+head) {
		t.Fatalf("offline fallback should keep the pinned manifest:\n%s", man)
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "more.md")); !os.IsNotExist(statErr) {
		t.Fatalf("offline fallback should keep existing outputs at the cached pin, stat err = %v", statErr)
	}
	audit := auditContentsForURL(t, srv.URL)
	if strings.Contains(audit, "auto-advance ") {
		t.Fatalf("offline fallback must not record an advance:\n%s", audit)
	}
}

func TestSync_AutoAdvancePinMovedButEmitFailsReturnsDistinctExitAndRecovers(t *testing.T) {
	requireGit(t)
	setTestXDG(t)

	worktree, _, head := makeUpdateRepo(t)
	srv := serveCanonicalRepo(t, worktree)
	ws := writeURLWorkspace(t, srv.URL, head, "main", nil)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("initial sync: %v\nstderr: %s", err, errOut)
	}

	brokenSHA := commitFile(t, worktree, "skills/broken/oops.txt", "not a skill\n", "broken skill")
	srv.PushMain(t)

	_, errOut, err := runSync(t, ws)
	if err == nil {
		t.Fatal("expected auto-advance sync to fail after re-pinning to the broken commit")
	}
	if code := MapExit(err); code != exitUpdatePinMoved {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitUpdatePinMoved, errOut)
	}
	if !strings.Contains(string(readManifest(t, ws)), "commit: "+brokenSHA) {
		t.Fatalf("manifest should show the moved pin after the failed sync:\n%s", string(readManifest(t, ws)))
	}

	if err := os.RemoveAll(filepath.Join(worktree, "skills", "broken")); err != nil {
		t.Fatalf("remove broken skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "rules", "recovered.md"), []byte("Recovered.\n"), 0o644); err != nil {
		t.Fatalf("write recovered rule: %v", err)
	}
	mustGit(t, worktree, "add", "-A")
	mustGit(t, worktree, "commit", "--quiet", "-m", "recovery")
	fixedSHA := mustGit(t, worktree, "rev-parse", "HEAD")
	srv.PushMain(t)

	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("re-run sync after fixing upstream: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(string(readManifest(t, ws)), "commit: "+fixedSHA) {
		t.Fatalf("manifest should advance to the recovery commit:\n%s", string(readManifest(t, ws)))
	}
	if _, statErr := os.Stat(filepath.Join(ws, ".claude", "rules", "agent-sync", "recovered.md")); statErr != nil {
		t.Fatalf("expected recovered rule after re-run: %v", statErr)
	}
}

func TestSyncCommand_FrozenFlagRegistered(t *testing.T) {
	cmd := newSyncCommand(RootDeps{})

	flag := cmd.Flags().Lookup("frozen")
	if flag == nil {
		t.Fatal("--frozen flag not registered")
	}
	if flag.Usage != "Disable auto-advance; sync at the pinned commit only" {
		t.Fatalf("unexpected help text: %q", flag.Usage)
	}
}

func TestSyncCommand_FrozenFlagParsesWithOtherBoolFlags(t *testing.T) {
	cmd := newSyncCommand(RootDeps{})
	if err := cmd.ParseFlags([]string{"--frozen", "--best-effort", "--post-merge"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	for _, name := range []string{"frozen", "best-effort", "post-merge"} {
		got, err := cmd.Flags().GetBool(name)
		if err != nil {
			t.Fatalf("GetBool(%q): %v", name, err)
		}
		if !got {
			t.Fatalf("--%s should parse as true", name)
		}
	}
}
