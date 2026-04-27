package adapterkit

import (
	"errors"
	"strings"
	"testing"

	contract "github.com/aienvs/aienvs/internal/adapter/contract"
)

// TestParseInboundMessage_RejectsTrailingBytes covers the smuggled-frame
// case: a payload containing two concatenated JSON envelopes must be
// rejected by parseInboundMessage. The earlier dec.More() check happened
// to handle this case, but the second-Decode pattern is the standard
// idiomatic check for "exactly one JSON value" — it returns a typed
// error rather than relying on More()'s discriminator semantics.
//
// This test pairs with TestParseInboundMessage_AdapterkitAndContractAgree
// below to ensure the runtime and adapterkit reject the same payloads.
func TestParseInboundMessage_RejectsTrailingBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "two concatenated objects",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}{"jsonrpc":"2.0","method":"initialized"}`,
		},
		{
			name: "object plus garbage",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}garbage`,
		},
		{
			name: "object plus number",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}42`,
		},
		{
			name: "object plus second whitespace-prefixed object",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}  {"k":1}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseInboundMessage([]byte(tc.raw))
			if err == nil {
				t.Fatalf("parseInboundMessage(%q) returned nil, want trailing-bytes error", tc.raw)
			}
			if !strings.Contains(err.Error(), "trailing bytes") &&
				!strings.Contains(err.Error(), "parse envelope") {
				t.Fatalf("parseInboundMessage error=%v; want trailing-bytes or parse-envelope error", err)
			}
		})
	}
}

// TestParseInboundMessage_AcceptsTrailingWhitespace confirms that
// trailing whitespace after a single JSON value is accepted — a peer
// that pads frames is benign, only a second JSON value (or garbage) is
// rejected. This mirrors contract.ParseMessage behavior.
func TestParseInboundMessage_AcceptsTrailingWhitespace(t *testing.T) {
	t.Parallel()

	raw := `{"jsonrpc":"2.0","method":"initialized"}   ` + "\r\n\t"
	msg, err := parseInboundMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parseInboundMessage(%q) err=%v", raw, err)
	}
	if msg.method != MethodInitialized {
		t.Fatalf("method=%q, want %q", msg.method, MethodInitialized)
	}
}

// TestParseInboundMessage_AdapterkitAndContractAgree asserts that for
// every test case, adapterkit's parseInboundMessage and the runtime's
// contract.ParseMessage classify validity the same way. This is the
// invariant the copilot review flagged: the two parsers must agree on
// what's a valid envelope, otherwise an adapter could accept a payload
// the runtime would reject (or vice versa) and create a protocol drift.
func TestParseInboundMessage_AdapterkitAndContractAgree(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "single valid notification",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}`,
		},
		{
			name: "two concatenated notifications",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}{"jsonrpc":"2.0","method":"initialized"}`,
		},
		{
			name: "trailing garbage",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}xyz`,
		},
		{
			name: "trailing number",
			raw:  `{"jsonrpc":"2.0","method":"initialized"}123`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, adapterErr := parseInboundMessage([]byte(tc.raw))
			_, contractErr := contract.ParseMessage([]byte(tc.raw))

			adapterRejected := adapterErr != nil
			contractRejected := contractErr != nil
			if adapterRejected != contractRejected {
				t.Fatalf("validity disagreement on %q:\n  adapterkit err=%v\n  contract err=%v",
					tc.raw, adapterErr, contractErr)
			}
			if contractRejected && !errors.Is(contractErr, contract.ErrInvalidEnvelope) {
				t.Fatalf("contract.ParseMessage err=%v; want ErrInvalidEnvelope", contractErr)
			}
		})
	}
}
