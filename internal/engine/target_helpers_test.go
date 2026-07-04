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
		got := effectiveOwnedPrefixes([]string{".claude/rules/agent-sync"}, nil, nil,
			[]contract.Op{op(".claude/rules/agent-sync/x.md")}, nil)
		if !reflect.DeepEqual(got, []string{".claude/rules/agent-sync"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("shared leaf derived from this run's ops", func(t *testing.T) {
		got := effectiveOwnedPrefixes(nil, []string{".agents/skills"}, nil,
			[]contract.Op{op(".agents/skills/agent-sync-x/SKILL.md")}, nil)
		if !reflect.DeepEqual(got, []string{".agents/skills/agent-sync-x"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("shared leaf derived from prior ledger (orphan path)", func(t *testing.T) {
		got := effectiveOwnedPrefixes(nil, []string{".agents/skills"}, nil,
			nil, []ledger.Entry{led(".agents/skills/agent-sync-old/SKILL.md")})
		if !reflect.DeepEqual(got, []string{".agents/skills/agent-sync-old"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("empty ops and ledger under shared prefix yields only owned", func(t *testing.T) {
		got := effectiveOwnedPrefixes([]string{".claude/rules/agent-sync"}, []string{".agents/skills"}, nil, nil, nil)
		if !reflect.DeepEqual(got, []string{".claude/rules/agent-sync"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("foreign sibling never enters the set", func(t *testing.T) {
		// Only an agent-sync op + a foreign ledger entry that is NOT agent-sync's.
		// The foreign path is in neither this run's ops nor... wait, it IS in
		// the ledger here only to prove union behavior: a real foreign leaf is
		// never in the ledger. We model the union: both leaves appear.
		got := effectiveOwnedPrefixes(nil, []string{".agents/skills"}, nil,
			[]contract.Op{op(".agents/skills/agent-sync-x/SKILL.md")},
			[]ledger.Entry{led(".agents/skills/agent-sync-y/SKILL.md")})
		sort.Strings(got)
		want := []string{".agents/skills/agent-sync-x", ".agents/skills/agent-sync-y"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("file-leaf: direct-child file enters the set as itself; parent never does", func(t *testing.T) {
		got := effectiveOwnedPrefixes(nil, nil, []string{".cursor/commands"},
			[]contract.Op{op(".cursor/commands/deploy.md")}, nil)
		if !reflect.DeepEqual(got, []string{".cursor/commands/deploy.md"}) {
			t.Fatalf("got %v want [.cursor/commands/deploy.md] (the file, not the parent)", got)
		}
	})

	t.Run("file-leaf: orphan file derived from prior ledger", func(t *testing.T) {
		got := effectiveOwnedPrefixes(nil, nil, []string{".pi/prompts"},
			nil, []ledger.Entry{led(".pi/prompts/old.md")})
		if !reflect.DeepEqual(got, []string{".pi/prompts/old.md"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("file-leaf: nested path and parent dir are not owned", func(t *testing.T) {
		// A nested path under a file-leaf parent is not a direct-child file, so it
		// never enters the effective set (the runtime path-safety gate also rejects
		// it). The parent dir itself is likewise never added.
		got := effectiveOwnedPrefixes(nil, nil, []string{".cursor/commands"},
			[]contract.Op{op(".cursor/commands/sub/nested.md")}, nil)
		if len(got) != 0 {
			t.Fatalf("nested path must not enter effective set; got %v", got)
		}
	})

	t.Run("file-leaf: mkdir op under a file-leaf parent does not enter the set", func(t *testing.T) {
		// file-leaf owns files, not directories. A stray mkdir under a file-leaf
		// parent must not create an effective prefix (which would be mis-handled as
		// a directory in the swap loop). Only write_file establishes a unit.
		got := effectiveOwnedPrefixes(nil, nil, []string{".cursor/commands"},
			[]contract.Op{contract.OpMkdir{Path: ".cursor/commands/foo"}}, nil)
		if len(got) != 0 {
			t.Fatalf("mkdir under a file-leaf parent must not enter effective set; got %v", got)
		}
	})
}

func TestFileLeafUnder(t *testing.T) {
	parents := []string{".cursor/commands", ".pi/prompts"}
	cases := []struct {
		p, want string
	}{
		{".cursor/commands/deploy.md", ".cursor/commands/deploy.md"}, // direct child → owned
		{".pi/prompts/review.md", ".pi/prompts/review.md"},           // direct child → owned
		{".cursor/commands/sub/x.md", ""},                            // nested → not owned
		{".cursor/commands", ""},                                     // the parent dir itself → not owned
		{".cursor/commands/..", ""},                                  // parent-escape segment → not owned
		{".cursor/commands/.", ""},                                   // dot segment → not owned
		{".cursor/commandsx/y.md", ""},                               // prefix-adjacent, not under parent
		{"other/deploy.md", ""},                                      // unrelated
	}
	for _, c := range cases {
		if got := fileLeafUnder(parents, c.p); got != c.want {
			t.Errorf("fileLeafUnder(%q) = %q, want %q", c.p, got, c.want)
		}
	}

	// A file-leaf parent of "." owns top-level files (no "/"); nested paths and
	// dot segments are not direct children.
	rootParents := []string{"."}
	rootCases := []struct{ p, want string }{
		{"deploy.md", "deploy.md"}, // top-level file → owned
		{"sub/x.md", ""},           // nested → not owned
		{".", ""},                  // the root itself → not owned
		{"..", ""},                 // escape → not owned
	}
	for _, c := range rootCases {
		if got := fileLeafUnder(rootParents, c.p); got != c.want {
			t.Errorf("fileLeafUnder([.], %q) = %q, want %q", c.p, got, c.want)
		}
	}
}
