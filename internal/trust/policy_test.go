package trust

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func baseInput() DecideInput {
	return DecideInput{
		URL:         urlX,
		ResolvedSHA: shaA,
		Now:         time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		Actor:       "tester",
		Hostname:    "test-host",
		Source:      SourceCLI,
	}
}

func TestDecideCIPinMatch(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.ManifestTrustedSHA = shaA
	in.TTY = false

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceed {
		t.Errorf("Kind = %q, want proceed", d.Kind)
	}
}

func TestDecideCIPinMismatch(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.ManifestTrustedSHA = shaB // manifest says B, resolved is A.
	in.TTY = false

	d, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired", err)
	}
	if d.Kind != KindDecisionRequired {
		t.Errorf("Kind = %q, want decision-required", d.Kind)
	}
	if d.Remediation == "" {
		t.Error("expected non-empty Remediation")
	}
}

func TestDecideCIFirstURLFailsClosed(t *testing.T) {
	t.Parallel()

	// No manifest pin, no state — CI must not guess.
	in := baseInput()
	in.TTY = false

	d, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired", err)
	}
	if d.Kind != KindDecisionRequired {
		t.Errorf("Kind = %q, want decision-required", d.Kind)
	}
}

func TestDecideCIAcceptNewSourceMatches(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.Flags.AcceptNewSource = shaA // matches resolved.

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceed {
		t.Errorf("Kind = %q, want proceed", d.Kind)
	}
	if !strings.Contains(d.AuditEcho, "Trusting new source") {
		t.Errorf("AuditEcho = %q, want audit line", d.AuditEcho)
	}
}

func TestDecideCIAcceptNewSourceMismatch(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.Flags.AcceptNewSource = shaB // user said trust B, resolved is A.

	_, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired (mismatch between --accept-new-source and resolved)", err)
	}
}

func TestDecideCIAcceptAnyWithoutPeerGate(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.Flags.AcceptAny = true
	in.Flags.AcceptAnyPeerGate = false

	_, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired (--accept-new-source=any requires peer gate)", err)
	}
}

func TestDecideCIAcceptAnyWithPeerGate(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.Flags.AcceptAny = true
	in.Flags.AcceptAnyPeerGate = true

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceed {
		t.Errorf("Kind = %q, want proceed", d.Kind)
	}
	if !strings.Contains(d.AuditEcho, "Trusting new source") {
		t.Errorf("AuditEcho = %q, want audit line", d.AuditEcho)
	}
}

func TestDecideInteractiveFirstURL(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = true

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindPromptFirstURL {
		t.Errorf("Kind = %q, want prompt-first-url", d.Kind)
	}
}

func TestDecideInteractiveKnownURLSameSHA(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = true
	in.State = State{CurrentSHA: shaA, LastOp: OpTrust}

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceed {
		t.Errorf("Kind = %q, want proceed", d.Kind)
	}
}

func TestDecideInteractiveKnownURLNewSHA(t *testing.T) {
	t.Parallel()

	// Per plan decision #9: sync NEVER prompts mid-sync on known-URL-new-SHA;
	// emits reminder, appends to pending, continues with existing trusted_sha.
	in := baseInput()
	in.TTY = true
	in.State = State{CurrentSHA: shaB, LastOp: OpTrust}
	in.ManifestTrustedSHA = shaB

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceedWithReminder {
		t.Errorf("Kind = %q, want proceed-with-reminder", d.Kind)
	}
	if d.Reminder == "" {
		t.Error("expected non-empty Reminder")
	}
	if d.AppendPending.URL != urlX || d.AppendPending.NewSHA != shaA || d.AppendPending.OldSHA != shaB {
		t.Errorf("AppendPending = %+v, want url=%q new=%q old=%q", d.AppendPending, urlX, shaA, shaB)
	}
	if d.TrustedSHA != shaB {
		t.Errorf("TrustedSHA = %q, want %q", d.TrustedSHA, shaB)
	}
}

func TestDecideRevokedBlocksEvenOnTTY(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = true
	in.State = State{Revoked: true, LastOp: OpRevoke}

	d, err := Decide(in)
	if !errors.Is(err, ErrRevokedTrustAnchor) {
		t.Fatalf("err = %v, want ErrRevokedTrustAnchor", err)
	}
	if d.Kind != KindRevokedBlock {
		t.Errorf("Kind = %q, want revoked-block", d.Kind)
	}
}

func TestDecideRevokedBlocksInCI(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.State = State{Revoked: true, LastOp: OpRevoke}

	_, err := Decide(in)
	if !errors.Is(err, ErrRevokedTrustAnchor) {
		t.Fatalf("err = %v, want ErrRevokedTrustAnchor", err)
	}
}

func TestDecideAllowNewSHAsActiveAutoPromotes(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = true
	in.State = State{
		CurrentSHA:     shaB,
		LastOp:         OpTrust,
		AllowNewSHAsOn: true,
		// cooldown expires well after Now (2026-05-10).
		AllowNewSHAsCooldownUntil: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceedAutoPromote {
		t.Errorf("Kind = %q, want proceed-auto-promote", d.Kind)
	}
	if d.AppendTrustLog.Op != OpPromote {
		t.Errorf("AppendTrustLog.Op = %q, want promote", d.AppendTrustLog.Op)
	}
	if d.AppendTrustLog.SHA != shaA || d.AppendTrustLog.PrevSHA != shaB {
		t.Errorf("AppendTrustLog sha/prev = %q/%q, want %q/%q",
			d.AppendTrustLog.SHA, d.AppendTrustLog.PrevSHA, shaA, shaB)
	}
}

func TestDecideAllowNewSHAsExpiredFallsBackToReminder(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = true
	in.State = State{
		CurrentSHA:                shaB,
		LastOp:                    OpTrust,
		AllowNewSHAsOn:            true,
		AllowNewSHAsCooldownUntil: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), // past.
	}

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceedWithReminder {
		t.Errorf("Kind = %q, want proceed-with-reminder (cooldown expired)", d.Kind)
	}
}

func TestDecideAllowNewSHAsIndefinite(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = true
	in.State = State{
		CurrentSHA:     shaB,
		LastOp:         OpTrust,
		AllowNewSHAsOn: true,
		// Zero cooldown-until == indefinite.
	}

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceedAutoPromote {
		t.Errorf("Kind = %q, want proceed-auto-promote (indefinite)", d.Kind)
	}
}

func TestDecideMapExitCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		want int
	}{
		{ErrRevokedTrustAnchor, ExitRevokedTrustAnchor},
		{ErrTrustDecisionRequired, ExitTrustDecisionRequired},
		{ErrFirstUseDenied, ExitFirstUseDenied},
		{errors.New("other"), 1},
		{nil, 0},
	}
	for _, tc := range cases {
		if got := ExitCodeFor(tc.err); got != tc.want {
			t.Errorf("ExitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
