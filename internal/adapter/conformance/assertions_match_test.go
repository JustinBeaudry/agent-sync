package conformance

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

func rec(op, path string) contract.OpRecord {
	return contract.OpRecord{Op: contract.OpKind(op), Path: path}
}

func TestMatchOps_Ordered(t *testing.T) {
	a := rec("mkdir", ".echo")
	b := rec("write_file", ".echo/x.md")
	c := rec("write_file", ".echo/y.md")

	tests := []struct {
		name        string
		expected    []contract.OpRecord
		actual      []contract.OpRecord
		wantOK      bool
		wantMissing int
		wantExtra   int
	}{
		{"identical", []contract.OpRecord{a, b}, []contract.OpRecord{a, b}, true, 0, 0},
		{"reordered fails in strict mode", []contract.OpRecord{a, b}, []contract.OpRecord{b, a}, false, 2, 2},
		{"actual longer", []contract.OpRecord{a}, []contract.OpRecord{a, c}, false, 0, 1},
		{"expected longer", []contract.OpRecord{a, b}, []contract.OpRecord{a}, false, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, missing, extra := MatchOps(tt.expected, tt.actual, true)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (missing=%v extra=%v)", ok, tt.wantOK, missing, extra)
			}
			if len(missing) != tt.wantMissing {
				t.Fatalf("missing = %d, want %d", len(missing), tt.wantMissing)
			}
			if len(extra) != tt.wantExtra {
				t.Fatalf("extra = %d, want %d", len(extra), tt.wantExtra)
			}
		})
	}
}

func TestMatchOps_UnorderedIgnoresOrder(t *testing.T) {
	a := rec("mkdir", ".echo")
	b := rec("write_file", ".echo/x.md")
	ok, missing, extra := MatchOps([]contract.OpRecord{a, b}, []contract.OpRecord{b, a}, false)
	if !ok {
		t.Fatalf("unordered match should pass regardless of order (missing=%v extra=%v)", missing, extra)
	}
}

func TestMatchError_NamedClasses(t *testing.T) {
	tests := []struct {
		expected string
		err      error
		want     bool
	}{
		{"ErrAdapterCookieMissing", adapter.ErrAdapterCookieMissing, true},
		{"ErrAdapterCapabilityLied", adapter.ErrAdapterCapabilityLied, true},
		{"ErrAdapterTimeout", adapter.ErrAdapterTimeout, true},
		{"ErrFrameTooLarge", contract.ErrFrameTooLarge, true},
		{"ErrAdapterTimeout", adapter.ErrAdapterCookieMissing, false}, // mismatched class
		{"UnknownExpectedName", adapter.ErrAdapterTimeout, false},     // default branch
		{"ErrAdapterTimeout", nil, false},                             // nil err short-circuit
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := MatchError(tt.expected, tt.err); got != tt.want {
				t.Fatalf("MatchError(%q, %v) = %v, want %v", tt.expected, tt.err, got, tt.want)
			}
		})
	}
}
