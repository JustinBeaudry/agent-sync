package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractCookieFromResult_TopLevelOnly confirms MAINT-005: the
// canonical location for the echoed cookie is the top-level "cookie"
// field. _meta.cookie is no longer accepted.
func TestExtractCookieFromResult_TopLevelOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"top-level", `{"cookie":"abc123","other":"x"}`, "abc123"},
		{"meta-only-now-rejected", `{"_meta":{"cookie":"abc123"}}`, ""},
		{"missing", `{"server":"echo"}`, ""},
		{"empty-string", `{"cookie":""}`, ""},
		{"malformed", `not-json`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCookieFromResult(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Errorf("extractCookieFromResult(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestCookieMismatchErrorDoesNotLeakCookieValue confirms SEC-001: the
// error returned on cookie mismatch must not reveal the cookie value
// the runtime expected, since that's the secret the cookie is meant
// to protect.
func TestCookieMismatchErrorDoesNotLeakCookieValue(t *testing.T) {
	t.Parallel()

	// The mismatch error path wraps ErrAdapterCookieMismatch with no
	// extra context — so the surfaced error string is exactly the
	// sentinel message. Confirm the sentinel itself is value-free.
	msg := ErrAdapterCookieMismatch.Error()
	if strings.Contains(msg, "echoed") || strings.Contains(msg, "want") {
		t.Errorf("cookie sentinel must not name the values it compared: %q", msg)
	}
}

// TestResolveTimeouts_FillsZeroFields confirms MAINT-003: the helper
// is a pure function over its input.
func TestResolveTimeouts_FillsZeroFields(t *testing.T) {
	t.Parallel()

	defaults := DefaultSubprocessTimeouts()

	got := resolveTimeouts(SubprocessTimeouts{})
	if got != defaults {
		t.Errorf("zero input: got %+v, want %+v", got, defaults)
	}

	custom := SubprocessTimeouts{Handshake: 0, Emit: defaults.Emit * 2, Shutdown: 0}
	got = resolveTimeouts(custom)
	if got.Handshake != defaults.Handshake {
		t.Errorf("Handshake: got %v, want %v", got.Handshake, defaults.Handshake)
	}
	if got.Emit != defaults.Emit*2 {
		t.Errorf("Emit: got %v, want %v (preserved)", got.Emit, defaults.Emit*2)
	}
	if got.Shutdown != defaults.Shutdown {
		t.Errorf("Shutdown: got %v, want %v", got.Shutdown, defaults.Shutdown)
	}
}
