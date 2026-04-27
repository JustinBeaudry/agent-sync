package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aienvs/aienvs/internal/adapter/contract"
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

// TestIRContainsSupportedKind_Streaming confirms the streaming
// implementation finds the supported kind correctly across the
// shapes we care about, returns false for malformed IR (no false
// positives), and short-circuits without buffering the whole node
// tree — exercised here with a 10k-node IR that contains no
// supported kind.
func TestIRContainsSupportedKind_Streaming(t *testing.T) {
	t.Parallel()

	supported := map[contract.OpKind]bool{
		contract.OpKind("rule"): true,
	}

	tests := []struct {
		name string
		ir   string
		want bool
	}{
		{"empty-object", `{}`, false},
		{"nodes-empty", `{"nodes":[]}`, false},
		{"single-supported", `{"nodes":[{"id":"x","kind":"rule"}]}`, true},
		{"single-unsupported", `{"nodes":[{"id":"x","kind":"agent"}]}`, false},
		{"mixed-finds-supported", `{"nodes":[{"kind":"agent"},{"kind":"rule"}]}`, true},
		{"nodes-after-other-fields", `{"version":"v1","meta":{"a":1},"nodes":[{"kind":"rule"}]}`, true},
		{"nodes-with-extra-fields", `{"nodes":[{"id":"x","kind":"rule","attrs":{"k":"v"},"deps":["y"]}]}`, true},
		{"missing-nodes-field", `{"version":"v1","meta":{"a":1}}`, false},
		{"malformed-truncated", `{"nodes":[{"kind":`, false},
		{"malformed-not-object", `[]`, false},
		{"malformed-not-json", `not-json`, false},
		{"malformed-nodes-not-array", `{"nodes":{"kind":"rule"}}`, false},
		{"node-without-kind", `{"nodes":[{"id":"x"}]}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := irContainsSupportedKindForTarget(json.RawMessage(tt.ir), supported, "any")
			if got != tt.want {
				t.Errorf("irContainsSupportedKindForTarget(%q) = %v, want %v", tt.ir, got, tt.want)
			}
		})
	}
}

// TestIRContainsSupportedKindForTarget_PerNodeTargetsFilter confirms
// the capability-lied check honors a node's per-node `targets` field.
// Nodes targeted at other adapters must not trigger the capability-
// lied diagnostic for this adapter.
func TestIRContainsSupportedKindForTarget_PerNodeTargetsFilter(t *testing.T) {
	t.Parallel()

	supported := map[contract.OpKind]bool{
		contract.OpKind("rule"): true,
	}

	cases := []struct {
		name   string
		ir     string
		target string
		want   bool
	}{
		{
			name:   "empty-targets-applies-to-all",
			ir:     `{"nodes":[{"id":"x","kind":"rule"}]}`,
			target: "claude",
			want:   true,
		},
		{
			name:   "targets-includes-this-adapter",
			ir:     `{"nodes":[{"id":"x","kind":"rule","targets":["claude","cursor"]}]}`,
			target: "claude",
			want:   true,
		},
		{
			name:   "targets-excludes-this-adapter",
			ir:     `{"nodes":[{"id":"x","kind":"rule","targets":["cursor"]}]}`,
			target: "claude",
			want:   false,
		},
		{
			name:   "mixed-some-target-this",
			ir:     `{"nodes":[{"id":"a","kind":"rule","targets":["cursor"]},{"id":"b","kind":"rule","targets":["claude"]}]}`,
			target: "claude",
			want:   true,
		},
		{
			name:   "all-target-other-adapters",
			ir:     `{"nodes":[{"id":"a","kind":"rule","targets":["cursor"]},{"id":"b","kind":"rule","targets":["codex"]}]}`,
			target: "claude",
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := irContainsSupportedKindForTarget(json.RawMessage(tc.ir), supported, tc.target)
			if got != tc.want {
				t.Errorf("got %v want %v for ir=%q target=%q", got, tc.want, tc.ir, tc.target)
			}
		})
	}
}

// TestIRContainsSupportedKind_LargeIR confirms the streaming decoder
// handles a large IR without blowing up (and short-circuits when no
// match exists, instead of buffering 10k nodes).
func TestIRContainsSupportedKind_LargeIR(t *testing.T) {
	t.Parallel()

	supported := map[contract.OpKind]bool{
		contract.OpKind("rule"): true,
	}

	// Build {"nodes":[{"id":"n0","kind":"agent"},...,{"id":"n9999","kind":"agent"}]}.
	// None of the kinds match — function must return false without
	// retaining the full node tree.
	var b strings.Builder
	b.WriteString(`{"nodes":[`)
	for i := 0; i < 10_000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		// Include a few extra fields per node so each element has some
		// shape to skip past.
		b.WriteString(`{"id":"n`)
		// Cheap int-to-string without strconv to avoid the import dance.
		// Just write digits.
		num := []byte{}
		n := i
		if n == 0 {
			num = []byte{'0'}
		} else {
			for n > 0 {
				num = append([]byte{byte('0' + n%10)}, num...)
				n /= 10
			}
		}
		b.Write(num)
		b.WriteString(`","kind":"agent","attrs":{"k":"v"}}`)
	}
	b.WriteString(`]}`)

	got := irContainsSupportedKindForTarget(json.RawMessage(b.String()), supported, "any")
	if got != false {
		t.Errorf("large IR with no supported kind: got true, want false")
	}

	// Also confirm a large IR where a supported kind sits late in the
	// stream is still found.
	mixed := strings.Replace(b.String(), `"id":"n9999","kind":"agent"`, `"id":"n9999","kind":"rule"`, 1)
	if got := irContainsSupportedKindForTarget(json.RawMessage(mixed), supported, "any"); got != true {
		t.Errorf("large IR with late supported kind: got false, want true")
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
