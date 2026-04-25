package ir

import (
	"errors"
	"testing"
)

func TestAllKindsClosedSet(t *testing.T) {
	t.Parallel()

	got := AllKinds()
	want := []Kind{
		KindAgentsMD, KindRule, KindSkill, KindCommand,
		KindPluginReference, KindMCPServerEntry,
	}
	if len(got) != len(want) {
		t.Fatalf("AllKinds returned %d kinds, want %d", len(got), len(want))
	}
	for i, k := range got {
		if k != want[i] {
			t.Errorf("AllKinds[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestValidateKind(t *testing.T) {
	t.Parallel()

	for _, k := range AllKinds() {
		if err := ValidateKind(k); err != nil {
			t.Errorf("ValidateKind(%q) unexpected error: %v", k, err)
		}
	}
	if err := ValidateKind(Kind("bogus")); err == nil {
		t.Error("ValidateKind(bogus) returned nil, want error")
	}
}

func TestIsValidID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"foo", true},
		{"f", true},
		{"foo-bar", true},
		{"foo_bar", true},
		{"a1b2c3", true},
		{"agents-md", true},
		{"123start-ok", true},
		// Boundary: max length 64.
		{"a234567890123456789012345678901234567890123456789012345678901234", true},   // 64 chars
		{"a2345678901234567890123456789012345678901234567890123456789012345", false}, // 65 chars

		{"", false},                // empty
		{"-leading-hyphen", false}, // must start alphanumeric
		{"_leading-underscore", false},
		{"UPPER", false},   // case-sensitive lowercase
		{"foo bar", false}, // no space
		{"foo/bar", false}, // no slash
		{"foo.bar", false}, // no dot
	}
	for _, tc := range cases {
		if got := IsValidID(tc.in); got != tc.want {
			t.Errorf("IsValidID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		ErrUnrecognizedFile,
		ErrInvalidID,
		ErrDuplicateID,
		ErrUnknownTarget,
		ErrFrontmatterParse,
		ErrUnknownFrontmatterField,
		ErrSkillMissingSKILL,
		ErrEmptyAgentsMD,
	}
	seen := map[string]bool{}
	for _, e := range sentinels {
		if e == nil {
			t.Fatal("nil sentinel")
		}
		msg := e.Error()
		if seen[msg] {
			t.Errorf("duplicate sentinel message: %q", msg)
		}
		seen[msg] = true
	}

	// Wrap-and-unwrap round trip.
	wrapped := errors.Join(ErrInvalidID, errors.New("extra"))
	if !errors.Is(wrapped, ErrInvalidID) {
		t.Error("errors.Is lost ErrInvalidID after Join")
	}
}
