package coverage

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestAnalyzeProjectLevelNoWarnings(t *testing.T) {
	kinds := []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindRule, ir.KindAgentsMD}
	got := Analyze(hierarchy.LevelProject, kinds, []string{"claude"})
	if len(got) != 0 {
		t.Fatalf("project level should be fully native, got warnings: %+v", got)
	}
}

func TestAnalyzeUserLevelClaudeCodexNative(t *testing.T) {
	// Claude (after scope-aware paths) and Codex (after the agents-md remap)
	// read every supported kind from a user-global location, so they have no
	// user-scope gap.
	kinds := []ir.Kind{ir.KindSkill, ir.KindCommand, ir.KindAgentsMD, ir.KindMCPServerEntry}
	if got := Analyze(hierarchy.LevelUser, kinds, []string{"claude", "codex"}); len(got) != 0 {
		t.Fatalf("claude/codex user level should be native, got: %+v", got)
	}
}

func TestAnalyzeUserLevelAntigravityRuleAndCommandWarn(t *testing.T) {
	// antigravity reads agents-md, skill, and mcp from ~/.gemini/ at user scope,
	// but has no user-global home for rule (.agent/rules) or command
	// (.agent/workflows) — those warn.
	got := Analyze(hierarchy.LevelUser, []ir.Kind{ir.KindRule, ir.KindCommand, ir.KindSkill, ir.KindAgentsMD, ir.KindMCPServerEntry}, []string{"antigravity"})
	if len(got) != 2 {
		t.Fatalf("expected 2 user-scope warnings (rule, command), got %d: %+v", len(got), got)
	}
	for _, w := range got {
		if w.Target != "antigravity" || w.Level != hierarchy.LevelUser {
			t.Errorf("unexpected warning: %+v", w)
		}
		if w.Kind != ir.KindRule && w.Kind != ir.KindCommand {
			t.Errorf("only rule and command should warn at user scope; got %q", w.Kind)
		}
	}
}

func TestAnalyzeDirectoryLevelAntigravityAgentsMDNative(t *testing.T) {
	// antigravity walks nested GEMINI.md/AGENTS.md (agents-md native), but not
	// nested rules/workflows/skills/mcp.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindAgentsMD}, []string{"antigravity"}); len(got) != 0 {
		t.Fatalf("antigravity agents-md is native at directory level, got: %+v", got)
	}
	got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindRule, ir.KindSkill}, []string{"antigravity"})
	if len(got) != 2 {
		t.Fatalf("expected 2 directory-scope warnings (rule, skill), got %d: %+v", len(got), got)
	}
}

func TestAnalyzeUserLevelCursorRuleAndAgentsMDWarn(t *testing.T) {
	// Cursor has no file-addressable user-global home for rules (User Rules are
	// app-settings/cloud only) or AGENTS.md; only ~/.cursor/mcp.json is
	// file-addressable. So rule and agents-md at user scope are inert and warn.
	got := Analyze(hierarchy.LevelUser, []ir.Kind{ir.KindRule, ir.KindAgentsMD}, []string{"cursor"})
	if len(got) != 2 {
		t.Fatalf("expected 2 cursor user-scope warnings, got %d: %+v", len(got), got)
	}
	for _, w := range got {
		if w.Target != "cursor" || w.Level != hierarchy.LevelUser {
			t.Errorf("warning = %+v, want cursor/user", w)
		}
	}
	// Deterministic ordering by kind (agents-md < rule).
	if got[0].Kind != ir.KindAgentsMD || got[1].Kind != ir.KindRule {
		t.Errorf("warnings not ordered by kind: %+v", got)
	}
}

func TestAnalyzeUserLevelCursorMCPNative(t *testing.T) {
	// ~/.cursor/mcp.json is the one file-addressable global Cursor config, so
	// mcp-server-entry at user scope is native and must not warn.
	if got := Analyze(hierarchy.LevelUser, []ir.Kind{ir.KindMCPServerEntry}, []string{"cursor"}); len(got) != 0 {
		t.Fatalf("cursor user-scope mcp should be native, got: %+v", got)
	}
}

func TestAnalyzeDirectoryLevelClaudeSkillWarns(t *testing.T) {
	got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"claude"})
	if len(got) != 1 {
		t.Fatalf("expected 1 warning for claude nested skill, got %d: %+v", len(got), got)
	}
	w := got[0]
	if w.Target != "claude" || w.Kind != ir.KindSkill || w.Level != hierarchy.LevelDirectory {
		t.Errorf("warning fields = %+v, want claude/skill/directory", w)
	}
}

func TestAnalyzeDirectoryLevelClaudeAgentsMDNative(t *testing.T) {
	// Claude reads nested CLAUDE.md, so agents-md at directory level is native.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindAgentsMD}, []string{"claude"}); len(got) != 0 {
		t.Fatalf("claude nested agents-md should be native, got: %+v", got)
	}
}

func TestAnalyzeDirectoryLevelCursorRuleNative(t *testing.T) {
	// Cursor reads nested .cursor/rules, so rule at directory level is native.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindRule}, []string{"cursor"}); len(got) != 0 {
		t.Fatalf("cursor nested rule should be native, got: %+v", got)
	}
	// Cursor reads nested .agents/skills, so skill at directory level is native too.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"cursor"}); len(got) != 0 {
		t.Fatalf("cursor nested skill should be native, got: %+v", got)
	}
	// But cursor does not read nested commands or mcp entries natively.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindCommand}, []string{"cursor"}); len(got) != 1 {
		t.Fatalf("cursor nested command should warn, got: %+v", got)
	}
}

func TestAnalyzeDirectoryLevelClaudeMixedKindsWarnsOnlyNonNative(t *testing.T) {
	// claude reads nested CLAUDE.md (agents-md) but not nested skills, so a
	// directory-level scope emitting both kinds must warn for skill only. Pins
	// the per-kind filtering against future edits to the native-support table.
	got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindAgentsMD, ir.KindSkill}, []string{"claude"})
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 warning (skill only), got %d: %+v", len(got), got)
	}
	w := got[0]
	if w.Target != "claude" || w.Kind != ir.KindSkill || w.Level != hierarchy.LevelDirectory {
		t.Errorf("warning = %+v, want claude/skill/directory", w)
	}
}

func TestAnalyzeUnknownTargetNoWarnings(t *testing.T) {
	// An adapter we have no table for must never produce false warnings.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"some-third-party"}); len(got) != 0 {
		t.Fatalf("unknown target must default to native, got: %+v", got)
	}
}

func TestAnalyzeMultipleTargetsAndKindsDeterministic(t *testing.T) {
	got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill, ir.KindCommand}, []string{"claude", "codex"})
	// claude: skill+command warn (2); codex: skill+command warn (2) → 4 total.
	if len(got) != 4 {
		t.Fatalf("expected 4 warnings, got %d: %+v", len(got), got)
	}
	// Deterministic ordering: stable by target, then kind.
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Target > cur.Target || (prev.Target == cur.Target && prev.Kind > cur.Kind) {
			t.Fatalf("warnings not deterministically ordered: %+v", got)
		}
	}
}
