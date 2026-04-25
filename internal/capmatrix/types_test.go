package capmatrix

import "testing"

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, s := range AllStatuses() {
		if err := Validate(s); err != nil {
			t.Errorf("Validate(%q) unexpected error: %v", s, err)
		}
	}
	if err := Validate(CapabilityStatus("bogus")); err == nil {
		t.Error("Validate(bogus) returned nil, want error")
	}
}

func TestCompareOrdering(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b CapabilityStatus
		want int // sign only
	}{
		{Unsupported, Partial, -1},
		{Partial, Supported, -1},
		{Unsupported, Supported, -1},
		{Supported, Supported, 0},
		{Partial, Partial, 0},
		{Supported, Partial, 1},
		{Supported, Unsupported, 1},
		{Partial, Unsupported, 1},
	}
	for _, tc := range cases {
		got := Compare(tc.a, tc.b)
		if !sameSign(got, tc.want) {
			t.Errorf("Compare(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b CapabilityStatus
		want CapabilityStatus
	}{
		{Supported, Supported, Supported},
		{Supported, Partial, Partial},
		{Supported, Unsupported, Unsupported},
		{Partial, Unsupported, Unsupported},
		{Unsupported, Supported, Unsupported},
	}
	for _, tc := range cases {
		got := Min(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("Min(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUnknownStatusSortsBelowUnsupported(t *testing.T) {
	t.Parallel()
	// An unknown status (e.g. corrupted adapter manifest) must NOT silently
	// equal Unsupported — Min should pick the unknown so required-checks
	// fail loudly rather than silently degrading.
	got := Min(Unsupported, CapabilityStatus("future"))
	if got != CapabilityStatus("future") {
		t.Errorf("Min picked %q, expected unknown to win as the lowest rank", got)
	}
}

func sameSign(a, b int) bool {
	switch {
	case a < 0 && b < 0:
		return true
	case a > 0 && b > 0:
		return true
	case a == 0 && b == 0:
		return true
	}
	return false
}
