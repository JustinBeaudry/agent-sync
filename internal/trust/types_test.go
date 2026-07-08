package trust

import (
	"errors"
	"strings"
	"testing"
)

func TestKindString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		k    Kind
		want string
	}{
		{KindProceed, "proceed"},
		{KindProceedWithReminder, "proceed-with-reminder"},
		{KindPromptFirstURL, "prompt-first-url"},
		{KindPromptNewSHA, "prompt-new-sha"},
		{KindProceedAutoPromote, "proceed-auto-promote"},
		{KindProceedAutoAdvance, "proceed-auto-advance"},
		{KindRevokedBlock, "revoked-block"},
		{KindDecisionRequired, "decision-required"},
		{Kind(99), "unknown(99)"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(tc.k), got, tc.want)
		}
	}
}

func TestIsSHA40(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"0123456789abcdef0123456789abcdef01234567", true},
		{"0123456789ABCDEF0123456789abcdef01234567", false},  // uppercase rejected
		{"0123456789abcdef0123456789abcdef0123456", false},   // 39 chars
		{"0123456789abcdef0123456789abcdef012345678", false}, // 41 chars
		{"", false},
		{"ghabcdefghabcdefghabcdefghabcdefghabcdef", false}, // non-hex
	}
	for _, tc := range cases {
		if got := IsSHA40(tc.in); got != tc.want {
			t.Errorf("IsSHA40(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidateOp(t *testing.T) {
	t.Parallel()

	for _, op := range []Op{OpTrust, OpPromote, OpRevoke, OpAllowNewSHAsOn, OpAllowNewSHAsOff} {
		if err := ValidateOp(op); err != nil {
			t.Errorf("ValidateOp(%q) unexpected error: %v", op, err)
		}
	}

	if err := ValidateOp(Op("bogus")); err == nil {
		t.Error("ValidateOp(bogus) returned nil, want error")
	}
}

func TestValidateEntry(t *testing.T) {
	t.Parallel()

	base := LogEntry{
		TSRaw:    "2026-05-01T12:00:00Z",
		Op:       OpTrust,
		URL:      "https://github.com/example/repo",
		SHA:      "0123456789abcdef0123456789abcdef01234567",
		PrevSHA:  "",
		Source:   SourceCLI,
		Actor:    "alice",
		Hostname: "host",
	}

	t.Run("valid trust entry", func(t *testing.T) {
		if err := ValidateEntry(base); err != nil {
			t.Errorf("ValidateEntry(base) unexpected error: %v", err)
		}
	})

	t.Run("trust op requires sha", func(t *testing.T) {
		e := base
		e.SHA = ""
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for trust op with empty sha")
		}
	})

	t.Run("revoke op rejects sha", func(t *testing.T) {
		e := base
		e.Op = OpRevoke
		e.PrevSHA = "0123456789abcdef0123456789abcdef01234567"
		// SHA left non-empty; should error.
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for revoke op with non-empty sha")
		}
	})

	t.Run("revoke op with empty sha and valid prev_sha is valid", func(t *testing.T) {
		e := base
		e.Op = OpRevoke
		e.SHA = ""
		e.PrevSHA = "0123456789abcdef0123456789abcdef01234567"
		if err := ValidateEntry(e); err != nil {
			t.Errorf("ValidateEntry(revoke) unexpected error: %v", err)
		}
	})

	t.Run("revoke op with empty prev_sha rejected", func(t *testing.T) {
		// Spec: revoke records carry the SHA being revoked in prev_sha
		// (audit data). Empty prev_sha is invalid even though sha is empty.
		e := base
		e.Op = OpRevoke
		e.SHA = ""
		e.PrevSHA = ""
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for revoke op with empty prev_sha")
		}
	})

	t.Run("promote op with empty prev_sha rejected", func(t *testing.T) {
		// Spec: promote replaces a previous SHA — prev_sha is required.
		e := base
		e.Op = OpPromote
		e.PrevSHA = ""
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for promote op with empty prev_sha")
		}
	})

	t.Run("promote op with valid prev_sha is valid", func(t *testing.T) {
		e := base
		e.Op = OpPromote
		e.PrevSHA = "fedcba9876543210fedcba9876543210fedcba98"
		if err := ValidateEntry(e); err != nil {
			t.Errorf("ValidateEntry(promote) unexpected error: %v", err)
		}
	})

	t.Run("unknown op rejected", func(t *testing.T) {
		e := base
		e.Op = Op("bogus")
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for unknown op")
		}
	})

	t.Run("empty url rejected", func(t *testing.T) {
		e := base
		e.URL = ""
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for empty url")
		}
	})

	t.Run("malformed prev_sha rejected", func(t *testing.T) {
		e := base
		e.Op = OpPromote
		e.PrevSHA = "not-a-sha"
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for malformed prev_sha")
		}
	})

	t.Run("non-RFC3339 ts rejected", func(t *testing.T) {
		// Spec requires RFC3339 ts; without strict parsing, compaction's
		// ts-asc sort breaks on zero/garbage timestamps.
		e := base
		e.TSRaw = "not-a-timestamp"
		if err := ValidateEntry(e); err == nil {
			t.Error("expected error for non-RFC3339 ts")
		}
	})

	t.Run("RFC3339 ts with sub-second precision is valid", func(t *testing.T) {
		// time.Parse(RFC3339, ...) accepts the canonical form. The spec
		// constrains writers to second-precision; readers tolerate richer.
		e := base
		e.TSRaw = "2026-05-01T12:00:00.123Z"
		if err := ValidateEntry(e); err != nil {
			t.Errorf("ValidateEntry(sub-second ts) unexpected error: %v", err)
		}
	})
}

func TestSentinelErrors(t *testing.T) {
	t.Parallel()

	// Each sentinel wraps cleanly.
	wrapped := errors.Join(ErrRevokedTrustAnchor, errors.New("extra"))
	if !errors.Is(wrapped, ErrRevokedTrustAnchor) {
		t.Error("errors.Is lost ErrRevokedTrustAnchor after Join")
	}

	// Ensure each has a distinct, non-empty message.
	seen := map[string]bool{}
	for _, err := range []error{ErrRevokedTrustAnchor, ErrTrustDecisionRequired, ErrFirstUseDenied} {
		msg := err.Error()
		if msg == "" {
			t.Errorf("sentinel %v has empty message", err)
		}
		if !strings.HasPrefix(msg, "trust:") {
			t.Errorf("sentinel %q missing trust: prefix", msg)
		}
		if seen[msg] {
			t.Errorf("sentinel message collision: %q", msg)
		}
		seen[msg] = true
	}
}

func TestExitCodesStable(t *testing.T) {
	t.Parallel()

	// Exit codes are a public contract per docs/spec/trust-store-v1.md.
	// Changing them is a breaking change.
	if ExitRevokedTrustAnchor != 3 {
		t.Errorf("ExitRevokedTrustAnchor drifted: got %d want 3", ExitRevokedTrustAnchor)
	}
	if ExitTrustDecisionRequired != 4 {
		t.Errorf("ExitTrustDecisionRequired drifted: got %d want 4", ExitTrustDecisionRequired)
	}
	if ExitFirstUseDenied != 5 {
		t.Errorf("ExitFirstUseDenied drifted: got %d want 5", ExitFirstUseDenied)
	}
}
