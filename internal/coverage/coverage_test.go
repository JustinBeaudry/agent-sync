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

func TestAnalyzeUserLevelNoWarnings(t *testing.T) {
	kinds := []ir.Kind{ir.KindSkill, ir.KindCommand}
	if got := Analyze(hierarchy.LevelUser, kinds, []string{"claude", "codex"}); len(got) != 0 {
		t.Fatalf("user level should be native, got: %+v", got)
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
	// But cursor does not read nested skills natively.
	if got := Analyze(hierarchy.LevelDirectory, []ir.Kind{ir.KindSkill}, []string{"cursor"}); len(got) != 1 {
		t.Fatalf("cursor nested skill should warn, got: %+v", got)
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
