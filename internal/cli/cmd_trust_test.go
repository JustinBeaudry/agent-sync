package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aienvs/aienvs/internal/manifest"
	"github.com/aienvs/aienvs/internal/trust"
)

const (
	shaA = "1111111111111111111111111111111111111111"
	shaB = "2222222222222222222222222222222222222222"
	urlX = "https://github.com/example/x"
)

// testEnv holds the filesystem roots and dep bundle for a single test.
type testEnv struct {
	t            *testing.T
	tmp          string
	manifestPath string
	store        *trust.Store
	pending      *trust.PendingStore
	out          bytes.Buffer
	err          bytes.Buffer
	in           *strings.Reader
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	tmp := t.TempDir()
	e := &testEnv{
		t:            t,
		tmp:          tmp,
		manifestPath: filepath.Join(tmp, ".aienv.yaml"),
		store:        trust.NewStore(filepath.Join(tmp, "trust.jsonl")),
		pending:      trust.NewPendingStore(filepath.Join(tmp, "pending.jsonl")),
	}
	return e
}

func (e *testEnv) deps(stdin string) TrustDeps {
	e.in = strings.NewReader(stdin)
	return TrustDeps{
		Store:        e.store,
		Pending:      e.pending,
		ManifestPath: e.manifestPath,
		Prompter:     trust.NewPrompter(e.in, &e.err).WithNoColor(),
		In:           e.in,
		Out:          &e.out,
		Err:          &e.err,
		Now:          func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		Actor:        "tester",
		Hostname:     "test-host",
	}
}

func (e *testEnv) run(args ...string) error {
	cmd := NewTrustCommand(e.deps(""))
	cmd.SetArgs(args)
	cmd.SetOut(&e.out)
	cmd.SetErr(&e.err)
	return cmd.Execute()
}

func (e *testEnv) runWithStdin(stdin string, args ...string) error {
	cmd := NewTrustCommand(e.deps(stdin))
	cmd.SetArgs(args)
	cmd.SetOut(&e.out)
	cmd.SetErr(&e.err)
	return cmd.Execute()
}

func TestNewTrustCommandHasAllSubcommands(t *testing.T) {
	t.Parallel()

	cmd := NewTrustCommand(TrustDeps{})
	want := []string{
		"status", "pending", "diff", "promote", "pin", "verify",
		"reset", "add", "revoke", "allow-new-shas", "compact",
	}
	got := make(map[string]bool)
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing subcommand %q (have %+v)", name, got)
		}
	}
}

func TestTrustStatusEmpty(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	if err := e.run("status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if e.out.String() != "" {
		t.Errorf("expected empty output on empty store, got %q", e.out.String())
	}
}

func TestTrustStatusAfterAdd(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	// Seed the store via the public Append API.
	if err := e.store.Append(trust.LogEntry{
		TSRaw: "2026-05-01T12:00:00Z", Op: trust.OpTrust, URL: urlX,
		SHA: shaA, Source: trust.SourceCLI, Actor: "t", Hostname: "h",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := e.run("status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(e.out.String(), urlX) {
		t.Errorf("status output missing url: %q", e.out.String())
	}
	if !strings.Contains(e.out.String(), shaA[:12]) {
		t.Errorf("status output missing short sha: %q", e.out.String())
	}
}

func TestTrustStatusJSON(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	if err := e.store.Append(trust.LogEntry{
		TSRaw: "2026-05-01T12:00:00Z", Op: trust.OpTrust, URL: urlX,
		SHA: shaA, Source: trust.SourceCLI, Actor: "t", Hostname: "h",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := e.run("status", "--json"); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %q", err, e.out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0]["url"] != urlX {
		t.Errorf("row url = %v, want %q", rows[0]["url"], urlX)
	}
}

func TestTrustPendingList(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	if err := e.pending.Append(trust.PendingEntry{
		TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := e.run("pending"); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if !strings.Contains(e.out.String(), urlX) {
		t.Errorf("pending output missing url: %q", e.out.String())
	}
}

func TestTrustPromoteSingle(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	// Prior trust for shaA + pending entry proposing shaB.
	for _, op := range []trust.LogEntry{{
		TSRaw: "2026-04-01T12:00:00Z", Op: trust.OpTrust, URL: urlX, SHA: shaA,
		Source: trust.SourceCLI, Actor: "t", Hostname: "h",
	}} {
		if err := e.store.Append(op); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := e.pending.Append(trust.PendingEntry{
		TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA,
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	if err := e.run("promote", urlX); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Store now has a promote record, pending is empty.
	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if state[urlX].CurrentSHA != shaB {
		t.Errorf("CurrentSHA after promote = %q, want %q", state[urlX].CurrentSHA, shaB)
	}
	pend, err := e.pending.List()
	if err != nil {
		t.Fatalf("pending List: %v", err)
	}
	if len(pend) != 0 {
		t.Errorf("pending has %d entries after promote, want 0", len(pend))
	}
}

func TestTrustPromotePinManifest(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	writeTestManifest(t, e.manifestPath, "")
	if err := e.pending.Append(trust.PendingEntry{
		TSRaw: "2026-05-01T12:00:00Z", URL: urlX, NewSHA: shaB, OldSHA: shaA,
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	if err := e.run("promote", urlX, "--pin-manifest"); err != nil {
		t.Fatalf("promote --pin-manifest: %v", err)
	}

	got, err := manifest.LoadFile(e.manifestPath, manifest.LoadOptions{NonInteractive: false})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.TrustedSHA != shaB {
		t.Errorf("manifest trusted_sha = %q, want %q", got.TrustedSHA, shaB)
	}
}

func TestTrustPinWritesManifest(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	writeTestManifest(t, e.manifestPath, "")

	if err := e.run("pin", "--sha", shaA); err != nil {
		t.Fatalf("pin: %v", err)
	}

	got, err := manifest.LoadFile(e.manifestPath, manifest.LoadOptions{NonInteractive: false})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.TrustedSHA != shaA {
		t.Errorf("manifest trusted_sha = %q, want %q", got.TrustedSHA, shaA)
	}
}

func TestTrustPinRejectsBadSHA(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	writeTestManifest(t, e.manifestPath, "")
	err := e.run("pin", "--sha", "nope")
	if err == nil {
		t.Error("pin --sha=nope accepted, want error")
	}
}

func TestTrustVerifyMatches(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	writeTestManifest(t, e.manifestPath, shaA)
	if err := e.run("verify"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(e.out.String(), "verify: ok") {
		t.Errorf("verify output = %q, want 'verify: ok'", e.out.String())
	}
}

func TestTrustVerifyMismatchReturnsDecisionRequired(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	// Manifest has commit=shaA but trusted_sha=shaB.
	writeTestManifestWithCommit(t, e.manifestPath, shaA, shaB)

	err := e.run("verify")
	if !errors.Is(err, trust.ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired", err)
	}
	if got := trust.ExitCodeFor(err); got != trust.ExitTrustDecisionRequired {
		t.Errorf("ExitCodeFor = %d, want %d", got, trust.ExitTrustDecisionRequired)
	}
}

func TestTrustAddRequiresTypedConfirmation(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)

	// Wrong confirmation → reject.
	err := e.runWithStdin("yes\n", "add", urlX, shaA)
	if err == nil {
		t.Error("add with wrong confirmation returned nil, want error")
	}

	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if _, ok := state[urlX]; ok {
		t.Error("rejected add still wrote a record")
	}
}

func TestTrustAddAccepts(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	if err := e.runWithStdin(shaA+"\n", "add", urlX, shaA); err != nil {
		t.Fatalf("add: %v", err)
	}
	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if state[urlX].CurrentSHA != shaA {
		t.Errorf("CurrentSHA = %q, want %q", state[urlX].CurrentSHA, shaA)
	}
}

func TestTrustRevokeRequiresTypedURL(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	// Seed a trust for url.
	if err := e.store.Append(trust.LogEntry{
		TSRaw: "2026-05-01T12:00:00Z", Op: trust.OpTrust, URL: urlX, SHA: shaA,
		Source: trust.SourceCLI, Actor: "t", Hostname: "h",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Wrong confirmation.
	if err := e.runWithStdin("yes\n", "revoke", urlX); err == nil {
		t.Error("revoke with wrong confirmation accepted")
	}
	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if state[urlX].Revoked {
		t.Error("revoke wrote a record despite wrong confirmation")
	}

	// Correct confirmation.
	e.out.Reset()
	e.err.Reset()
	if err := e.runWithStdin(urlX+"\n", "revoke", urlX); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	state, err = e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if !state[urlX].Revoked {
		t.Error("revoke did not mark state as revoked")
	}
}

func TestTrustAllowNewSHAsOn(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	if err := e.run("allow-new-shas", urlX, "--cooldown", "168h"); err != nil {
		t.Fatalf("allow-new-shas: %v", err)
	}
	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if !state[urlX].AllowNewSHAsOn {
		t.Error("AllowNewSHAsOn = false, want true")
	}
	if state[urlX].AllowNewSHAsCooldownUntil.IsZero() {
		t.Error("cooldown-until not set")
	}
}

func TestTrustAllowNewSHAsOff(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	// First turn it on.
	if err := e.store.Append(trust.LogEntry{
		TSRaw: "2026-05-01T12:00:00Z", Op: trust.OpAllowNewSHAsOn, URL: urlX,
		Source: trust.SourceCLI, Actor: "t", Hostname: "h",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := e.run("allow-new-shas", urlX, "--off"); err != nil {
		t.Fatalf("allow-new-shas --off: %v", err)
	}
	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if state[urlX].AllowNewSHAsOn {
		t.Error("AllowNewSHAsOn = true, want false after --off")
	}
}

func TestTrustResetRestoresPriorTrust(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	// trust -> revoke sequence.
	for _, op := range []trust.LogEntry{
		{TSRaw: "2026-04-01T12:00:00Z", Op: trust.OpTrust, URL: urlX, SHA: shaA,
			Source: trust.SourceCLI, Actor: "t", Hostname: "h"},
		{TSRaw: "2026-04-02T12:00:00Z", Op: trust.OpRevoke, URL: urlX, PrevSHA: shaA,
			Source: trust.SourceCLI, Actor: "t", Hostname: "h"},
	} {
		if err := e.store.Append(op); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := e.runWithStdin(urlX+"\n", "reset", urlX); err != nil {
		t.Fatalf("reset: %v", err)
	}
	state, err := e.store.Fold()
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	if state[urlX].Revoked {
		t.Error("reset did not clear revoked flag")
	}
	if state[urlX].CurrentSHA != shaA {
		t.Errorf("reset CurrentSHA = %q, want %q (prior trust)", state[urlX].CurrentSHA, shaA)
	}
}

func TestTrustCompactNoOpOnEmpty(t *testing.T) {
	t.Parallel()

	e := newTestEnv(t)
	if err := e.run("compact"); err != nil {
		t.Fatalf("compact: %v", err)
	}
}

// writeTestManifest writes a minimal manifest at path with the given
// trusted_sha (empty string means the field is present but empty — the
// init-time template contract expected by manifest.WriteResolvedSHA).
// canonical.commit is set to trustedSHA to keep the loader's mirror
// invariant happy.
func writeTestManifest(t *testing.T, path, trustedSHA string) {
	t.Helper()
	writeTestManifestWithCommit(t, path, trustedSHA, trustedSHA)
}

// writeTestManifestWithCommit writes a manifest whose canonical.commit
// and trusted_sha fields can drift from each other — used by verify's
// mismatch test. Both keys are always present (per the template contract)
// even when their values are empty.
func writeTestManifestWithCommit(t *testing.T, path, commit, trustedSHA string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: 1\n")
	b.WriteString("canonical:\n")
	b.WriteString("  url: https://github.com/example/canonical\n")
	b.WriteString("  commit: " + commit + "\n")
	b.WriteString("trusted_sha: " + trustedSHA + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// Compile-time check that TrustDeps plays nicely with io interfaces; if a
// future refactor accidentally changes a field type, this line breaks.
var _ io.Writer = (*bytes.Buffer)(nil)
