package manifest_test

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/manifest"
)

// The compose block is the opt-in for hierarchy composition (plan
// docs/plans/2026-07-01-002, decision D2). It is a strict, namespaced block:
// compose.cursor-rules-from-user turns on folding the user-scope Cursor rule
// layer into a project's .cursor/rules/. Absent/empty ⇒ false ⇒ current
// behavior.

func TestLoad_ComposeCursorRulesFromUser_True(t *testing.T) {
	src := []byte("version: 1\ncanonical:\n  local_dir: .agents\ntargets: [cursor]\ncompose:\n  cursor-rules-from-user: true\n")
	m, err := manifest.LoadBytes(src, manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !m.Compose.CursorRulesFromUser {
		t.Errorf("Compose.CursorRulesFromUser = false, want true")
	}
}

func TestLoad_ComposeAbsent_DefaultsFalse(t *testing.T) {
	src := []byte("version: 1\ncanonical:\n  local_dir: .agents\ntargets: [cursor]\n")
	m, err := manifest.LoadBytes(src, manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Compose.CursorRulesFromUser {
		t.Errorf("Compose.CursorRulesFromUser = true with no compose block, want false")
	}
}

func TestLoad_ComposeEmpty_DefaultsFalse(t *testing.T) {
	src := []byte("version: 1\ncanonical:\n  local_dir: .agents\ntargets: [cursor]\ncompose: {}\n")
	m, err := manifest.LoadBytes(src, manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Compose.CursorRulesFromUser {
		t.Errorf("Compose.CursorRulesFromUser = true with empty compose block, want false")
	}
}

func TestLoad_ComposeUnknownSubKey_Rejected(t *testing.T) {
	// Strict decoding must apply recursively: a bogus key inside compose: is
	// rejected just like an unknown top-level key. Guards against the sub-map
	// silently accepting typos (e.g. cursor_rules_from_user vs the hyphenated
	// form).
	src := []byte("version: 1\ncanonical:\n  local_dir: .agents\ntargets: [cursor]\ncompose:\n  bogus: true\n")
	if _, err := manifest.LoadBytes(src, manifest.LoadOptions{NonInteractive: true}); err == nil {
		t.Fatal("expected error for unknown key inside compose:, got nil")
	}
}
