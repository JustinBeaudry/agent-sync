package report

import (
	"strings"
	"testing"
)

func tr(target string, status TargetStatus) TargetReport {
	return TargetReport{Target: target, Status: status}
}

func TestSummarize_AllOK(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "abc", "T", ModeAtomic, []TargetReport{
		tr("claude", StatusOK), tr("cursor", StatusOK), tr("codex", StatusUnchanged),
	})
	if s.Outcome.ExitCode != 0 {
		t.Errorf("exit = %d want 0", s.Outcome.ExitCode)
	}
	if !strings.HasPrefix(s.Outcome.Line, "OK ") {
		t.Errorf("line = %q want OK prefix", s.Outcome.Line)
	}
}

func TestSummarize_BestEffortPartial(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "abc", "T", ModeBestEffort, []TargetReport{
		tr("a", StatusOK), tr("b", StatusOK), tr("c", StatusFailed),
	})
	if s.Outcome.ExitCode == 0 {
		t.Error("partial should be non-zero exit")
	}
	if s.Outcome.Line != "PARTIAL 2 ok, 1 failed" {
		t.Errorf("line = %q", s.Outcome.Line)
	}
}

func TestSummarize_AtomicRollbackIsFail(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "abc", "T", ModeAtomic, []TargetReport{
		tr("a", StatusRolledBack), tr("b", StatusRolledBack),
	})
	if !strings.HasPrefix(s.Outcome.Line, "FAIL") || s.Outcome.ExitCode != 1 {
		t.Errorf("atomic rollback should be FAIL/exit1; got %q/%d", s.Outcome.Line, s.Outcome.ExitCode)
	}
}

func TestSummarize_ZeroSuccessIsFail(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "abc", "T", ModeBestEffort, []TargetReport{tr("a", StatusFailed)})
	if !strings.HasPrefix(s.Outcome.Line, "FAIL") {
		t.Errorf("0 success should be FAIL; got %q", s.Outcome.Line)
	}
}

func TestSummarize_SortsTargetsDeterministically(t *testing.T) {
	t.Parallel()
	in := []TargetReport{tr("cursor", StatusOK), tr("claude", StatusOK), tr("codex", StatusOK)}
	s := Summarize("/ws", "abc", "T", ModeAtomic, in)
	got := []string{s.Targets[0].Target, s.Targets[1].Target, s.Targets[2].Target}
	want := []string{"claude", "codex", "cursor"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target order = %v want %v", got, want)
		}
	}
}

func TestRenderText_CarriesTokensAndTopLine(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "abc", "T", ModeBestEffort, []TargetReport{
		{Target: "a", Status: StatusOK, Counts: Counts{Written: 2}},
		{Target: "b", Status: StatusFailed, Error: "boom"},
	})
	out := RenderText(s)
	for _, want := range []string{"ok", "failed", "a", "b", "boom", "PARTIAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}
