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
	// Post-decision trust = ResolvedSHA, since the caller will append the
	// promote record and the new SHA becomes the effective trust.
	if d.TrustedSHA != shaA {
		t.Errorf("TrustedSHA = %q, want %q (post-decision = ResolvedSHA)", d.TrustedSHA, shaA)
	}
}

func TestDecideCIKnownURLDriftAcceptNewSourceMatches(t *testing.T) {
	t.Parallel()

	// Known URL whose SHA drifted, non-interactive, with --accept-new-source
	// matching the resolved SHA. Remediation tells users to do exactly this,
	// so it must work as the documented CI escape hatch.
	in := baseInput()
	in.TTY = false
	in.State = State{CurrentSHA: shaB, LastOp: OpTrust}
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

func TestDecideCIKnownURLDriftAcceptNewSourceMismatch(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.State = State{CurrentSHA: shaB, LastOp: OpTrust}
	in.Flags.AcceptNewSource = shaC // user said trust C, resolved is A.

	_, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired (mismatch between --accept-new-source and resolved)", err)
	}
}

func TestDecideCIKnownURLDriftAcceptAnyWithPeerGate(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.State = State{CurrentSHA: shaB, LastOp: OpTrust}
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

func TestDecideCIKnownURLDriftAcceptAnyWithoutPeerGate(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.State = State{CurrentSHA: shaB, LastOp: OpTrust}
	in.Flags.AcceptAny = true
	in.Flags.AcceptAnyPeerGate = false

	_, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired (--accept-new-source=any requires peer gate)", err)
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

func TestDecideAutoAdvanceAllowsHealthyFastForwardPinnedDrift(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.ManifestTrustedSHA = shaB
	in.Posture = PostureAllowNewSHAs
	in.FastForward = true
	in.StateLoaded = true

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Kind != KindProceedAutoAdvance {
		t.Fatalf("Kind = %q, want proceed-auto-advance", d.Kind)
	}
	if d.TrustedSHA != shaA {
		t.Fatalf("TrustedSHA = %q, want %q", d.TrustedSHA, shaA)
	}
	if d.AppendPending.URL != urlX || d.AppendPending.OldSHA != shaB || d.AppendPending.NewSHA != shaA {
		t.Fatalf("AppendPending = %+v, want url=%q old=%q new=%q", d.AppendPending, urlX, shaB, shaA)
	}
}

func TestDecideAutoAdvanceRequiresLoadedState(t *testing.T) {
	t.Parallel()

	// Fail-closed guard: the auto posture with a proven fast-forward must
	// still refuse when State was not loaded from the store, because a zero
	// State has Revoked==false and would silently skip the revoke check.
	in := baseInput()
	in.TTY = false
	in.ManifestTrustedSHA = shaB
	in.Posture = PostureAllowNewSHAs
	in.FastForward = true
	in.StateLoaded = false

	d, err := Decide(in)
	if !errors.Is(err, ErrTrustDecisionRequired) {
		t.Fatalf("err = %v, want ErrTrustDecisionRequired", err)
	}
	if d.Kind != KindDecisionRequired {
		t.Fatalf("Kind = %q, want decision-required", d.Kind)
	}
}

func TestDecideAutoAdvanceRevokeBeatsPostureWithLoadedState(t *testing.T) {
	t.Parallel()

	// Even with the auto posture, a proven fast-forward, and loaded state,
	// an active revoke must hard-block. This pins the revoke-before-posture
	// ordering so a future reorder is caught.
	in := baseInput()
	in.TTY = false
	in.ManifestTrustedSHA = shaB
	in.Posture = PostureAllowNewSHAs
	in.FastForward = true
	in.StateLoaded = true
	in.State = State{Revoked: true, LastOp: OpRevoke}

	d, err := Decide(in)
	if !errors.Is(err, ErrRevokedTrustAnchor) {
		t.Fatalf("err = %v, want ErrRevokedTrustAnchor", err)
	}
	if d.Kind != KindRevokedBlock {
		t.Fatalf("Kind = %q, want revoked-block", d.Kind)
	}
}

func TestDecideAutoAdvancePinnedDriftPostureMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		posture     Posture
		fastForward bool
		revoked     bool
		wantKind    Kind
		wantErr     error
	}{
		{
			name:        "default posture falls back to current gate",
			posture:     PostureDefault,
			fastForward: true,
			wantKind:    KindDecisionRequired,
			wantErr:     ErrTrustDecisionRequired,
		},
		{
			name:        "auto posture requires fast forward",
			posture:     PostureAllowNewSHAs,
			fastForward: false,
			wantKind:    KindDecisionRequired,
			wantErr:     ErrTrustDecisionRequired,
		},
		{
			name:        "revoked always blocks",
			posture:     PostureAllowNewSHAs,
			fastForward: true,
			revoked:     true,
			wantKind:    KindRevokedBlock,
			wantErr:     ErrRevokedTrustAnchor,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			in.TTY = false
			in.ManifestTrustedSHA = shaB
			in.Posture = tc.posture
			in.FastForward = tc.fastForward
			in.StateLoaded = true
			if tc.revoked {
				in.State = State{Revoked: true, LastOp: OpRevoke}
			}

			d, err := Decide(in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if d.Kind != tc.wantKind {
				t.Fatalf("Kind = %q, want %q", d.Kind, tc.wantKind)
			}
		})
	}
}

func TestDecideAutoAdvancePendingEntryAppendsOnce(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.TTY = false
	in.ManifestTrustedSHA = shaB
	in.Posture = PostureAllowNewSHAs
	in.FastForward = true
	in.StateLoaded = true

	d, err := Decide(in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	p := newPendingInDir(t)
	if err := p.Append(d.AppendPending); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := p.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("pending entries = %d, want 1", len(entries))
	}
	if entries[0].OldSHA != shaB || entries[0].NewSHA != shaA {
		t.Fatalf("pending entry = %+v, want old=%q new=%q", entries[0], shaB, shaA)
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
