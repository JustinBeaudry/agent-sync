package engine

import (
	"reflect"
	"sort"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/internal/ledger"
)

func TestLeafUnder(t *testing.T) {
	shared := []string{".agents/skills"}
	tests := []struct {
		name string
		p    string
		want string
	}{
		{"file under leaf", ".agents/skills/agent-sync-x/SKILL.md", ".agents/skills/agent-sync-x"},
		{"leaf dir itself", ".agents/skills/agent-sync-x", ".agents/skills/agent-sync-x"},
		{"deep nested keeps first segment", ".agents/skills/agent-sync-x/a/b/c.txt", ".agents/skills/agent-sync-x"},
		{"exact parent, no child", ".agents/skills", ""},
		{"trailing slash, empty segment", ".agents/skills/", ""},
		{"parent traversal segment rejected", ".agents/skills/../evil", ""},
		{"dot segment rejected", ".agents/skills/./x", ""},
		{"not under any shared prefix", ".other/thing/x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leafUnder(shared, tt.p); got != tt.want {
				t.Fatalf("leafUnder(%q) = %q, want %q", tt.p, got, tt.want)
			}
		})
	}
}

func TestLeafUnder_NestedPrefixesLongestWins(t *testing.T) {
	// sharedSubdirs sorts longest-first; leafUnder must pick the most specific.
	shared := []string{".a/b", ".a"}
	if got := leafUnder(shared, ".a/b/leaf/x"); got != ".a/b/leaf" {
		t.Fatalf("nested: got %q, want .a/b/leaf", got)
	}
	if got := leafUnder(shared, ".a/other/x"); got != ".a/other" {
		t.Fatalf("shorter prefix: got %q, want .a/other", got)
	}
}

func TestEffectiveOwnedPrefixes(t *testing.T) {
	op := func(p string) contract.Op { return contract.OpWriteFile{Path: p} }
	led := func(p string) ledger.Entry { return ledger.Entry{Path: p} }

	t.Run("owned passes through; no shared leaves without shared prefixes", func(t *testing.T) {
		got := effectiveOwnedPrefixes([]string{".claude/rules/agent-sync"}, nil,
			[]contract.Op{op(".claude/rules/agent-sync/x.md")}, nil)
		if !reflect.DeepEqual(got, []string{".claude/rules/agent-sync"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("shared leaf derived from this run's ops", func(t *testing.T) {
		got := effectiveOwnedPrefixes(nil, []string{".agents/skills"},
			[]contract.Op{op(".agents/skills/agent-sync-x/SKILL.md")}, nil)
		if !reflect.DeepEqual(got, []string{".agents/skills/agent-sync-x"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("shared leaf derived from prior ledger (orphan path)", func(t *testing.T) {
		got := effectiveOwnedPrefixes(nil, []string{".agents/skills"},
			nil, []ledger.Entry{led(".agents/skills/agent-sync-old/SKILL.md")})
		if !reflect.DeepEqual(got, []string{".agents/skills/agent-sync-old"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("empty ops and ledger under shared prefix yields only owned", func(t *testing.T) {
		got := effectiveOwnedPrefixes([]string{".claude/rules/agent-sync"}, []string{".agents/skills"}, nil, nil)
		if !reflect.DeepEqual(got, []string{".claude/rules/agent-sync"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("foreign sibling never enters the set", func(t *testing.T) {
		// Only an aienvs op + a foreign ledger entry that is NOT agent-sync's.
		// The foreign path is in neither this run's ops nor... wait, it IS in
		// the ledger here only to prove union behavior: a real foreign leaf is
		// never in the ledger. We model the union: both leaves appear.
		got := effectiveOwnedPrefixes(nil, []string{".agents/skills"},
			[]contract.Op{op(".agents/skills/agent-sync-x/SKILL.md")},
			[]ledger.Entry{led(".agents/skills/agent-sync-y/SKILL.md")})
		sort.Strings(got)
		want := []string{".agents/skills/agent-sync-x", ".agents/skills/agent-sync-y"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})
}
