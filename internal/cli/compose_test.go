package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/engine"
	"github.com/agent-sync/agent-sync/internal/hierarchy"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/manifest"
	"github.com/agent-sync/agent-sync/internal/report"
)

// userWarnKinds returns the coverage-warning kinds recorded on the user-scope
// outcome for the given target.
func userWarnKinds(outcomes []scopeOutcome, target string) map[ir.Kind]bool {
	got := map[ir.Kind]bool{}
	for _, o := range outcomes {
		if o.Scope.Level != hierarchy.LevelUser {
			continue
		}
		for _, w := range o.Warnings {
			if w.Target == target {
				got[w.Kind] = true
			}
		}
	}
	return got
}

func composeEngineOpts(rc *runtimeContext, now time.Time) engine.Options {
	return engine.Options{Mode: report.ModeAtomic, Now: func() time.Time { return now }, Logger: rc.Logger}
}

// TestCompose_UserSyncKeepsRuleWarningEvenWhenProjectWouldCompose pins the
// one-write-target rule: `sync --user` emits only the user scope and must not
// prepare a project scope just to decide whether to suppress warnings.
func TestCompose_UserSyncKeepsRuleWarningEvenWhenProjectWouldCompose(t *testing.T) {
	home, repo := composeTree(t)
	// User manifest (cursor) carrying BOTH a rule and an agents-md.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	writeWS(t, home, ".agents/AGENTS.md", "user standards\n")
	// Project opts in.
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	rc := newTestRuntime()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	outcomes, _, err := runHierarchySync(context.Background(), rc, repo, home,
		hierarchySyncOptions{IncludeUser: true, EngineOpts: composeEngineOpts(rc, now)}, now)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}

	got := userWarnKinds(outcomes, cursorTarget)
	if !got[ir.KindRule] {
		t.Error("user-scope Cursor rule warning should remain during a user-only sync")
	}
	if !got[ir.KindAgentsMD] {
		t.Error("user-scope Cursor agents-md warning should remain")
	}
}

func TestCompose_ActivationRootStopsUserComposition(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "ActualReality")
	repo := filepath.Join(workspaceRoot, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/from-user.md", "activation root must stop this user rule\n")
	workspaceManifest := "version: 1\n" +
		"scope: " + manifest.ScopeWorkspace + "\n" +
		"activation_root: true\n" +
		"canonical:\n" +
		"  local_dir: .agents\n" +
		"targets:\n" +
		"  - cursor\n"
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".agent-sync.yaml"), []byte(workspaceManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/project-only.md", "project rule\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "project-only"))
	mustNotExist(t, rulePath(repo, "from-user"))
}

func TestCompose_WorkspaceManifestDoesNotComposeUserRules(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/from-user.md", "workspace composition should not inherit user rules\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectWorkspaceManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/project-only.md", "project rule\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "project-only"))
	mustNotExist(t, rulePath(repo, "from-user"))
}

func TestCompose_GlobalManifestComposesAsProjectAlias(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/from-user.md", "global alias should inherit user rules\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectGlobalManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/project-only.md", "project rule\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "project-only"))
	mustExist(t, rulePath(repo, "from-user"))
}

// TestCompose_ComposedRuleDoesNotLeakToOtherAdapters is the regression guard for
// the empty-Targets over-delivery bug: a user rule authored with no frontmatter
// targets means "all adapters", but composition is Cursor-only (D1). In a
// project targeting [cursor, claude], the composed user rule must land ONLY in
// .cursor/rules/, never in claude's .claude/rules/.
func TestCompose_ComposedRuleDoesNotLeakToOtherAdapters(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/u.md", "user rule u\n") // empty Targets => all adapters
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifestCursorClaude), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	// Composed into cursor...
	mustExist(t, rulePath(repo, "u"))
	// ...but NOT into claude, even though claude supports rules and the user
	// rule's empty Targets would otherwise mean "all adapters".
	mustNotExist(t, claudeRulePath(repo, "u"))
	// The project's own rule c still reaches both targets natively.
	mustExist(t, rulePath(repo, "c"))
	mustExist(t, claudeRulePath(repo, "c"))
}

// TestCompose_ExplicitNonCursorUserRuleNotComposed: a user rule explicitly
// targeting a non-cursor adapter (targets: [codex]) is not a Cursor rule and
// must not be composed; a sibling all-adapters rule is.
func TestCompose_ExplicitNonCursorUserRuleNotComposed(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/keep.md", "compose me\n")
	writeWS(t, home, ".agents/rules/codexonly.md", "---\ntargets: [codex]\n---\ncodex only\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "keep"))         // all-adapters user rule composed
	mustNotExist(t, rulePath(repo, "codexonly")) // codex-only user rule not composed to cursor
}

// TestCompose_ColliyingUserRulesDoNotSuppressWarning pins user-only sync
// warning behavior: a `sync --user` run emits the user scope only, so a
// project-side composition setup must not hide the user-scope Cursor rule gap.
func TestCompose_CollidingUserRulesDoNotSuppressWarning(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/dup.md", "user dup\n") // collides with project 'dup'
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/dup.md", "project dup\n")

	rc := newTestRuntime()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	outcomes, _, err := runHierarchySync(context.Background(), rc, repo, home,
		hierarchySyncOptions{IncludeUser: true, EngineOpts: composeEngineOpts(rc, now)}, now)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}
	got := userWarnKinds(outcomes, cursorTarget)
	if !got[ir.KindRule] {
		t.Error("user-scope Cursor rule warning must NOT be suppressed when composition injected nothing (all user rules shadowed)")
	}
}

// TestCompose_KeepsUserRuleWarningWhenInactive: without the opt-in, the
// user-scope Cursor rule warning is unchanged (still surfaced).
func TestCompose_KeepsUserRuleWarningWhenInactive(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	writeWS(t, home, ".agents/AGENTS.md", "user standards\n")
	// Project does NOT opt in.
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeCursorNoOptIn), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	rc := newTestRuntime()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	outcomes, _, err := runHierarchySync(context.Background(), rc, repo, home,
		hierarchySyncOptions{IncludeUser: true, EngineOpts: composeEngineOpts(rc, now)}, now)
	if err != nil {
		t.Fatalf("runHierarchySync: %v", err)
	}

	got := userWarnKinds(outcomes, cursorTarget)
	if !got[ir.KindRule] {
		t.Error("user-scope Cursor rule warning should remain when composition is inactive")
	}
	if !got[ir.KindAgentsMD] {
		t.Error("user-scope Cursor agents-md warning should remain when composition is inactive")
	}
}

// composeProjectManifest is a project manifest targeting cursor with the
// hierarchy-composition opt-in enabled.
const composeProjectManifest = "version: 1\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - cursor\n" +
	"compose:\n" +
	"  cursor-rules-from-user: true\n"

const composeProjectWorkspaceManifest = "version: 1\n" +
	"scope: " + manifest.ScopeWorkspace + "\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - cursor\n" +
	"compose:\n" +
	"  cursor-rules-from-user: true\n"

const composeProjectGlobalManifest = "version: 1\n" +
	"scope: " + manifest.ScopeGlobal + "\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - cursor\n" +
	"compose:\n" +
	"  cursor-rules-from-user: true\n"

// composeCursorNoOptIn is the same project manifest without the opt-in.
const composeCursorNoOptIn = "version: 1\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - cursor\n"

const composeUserManifestCursor = "version: 1\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - cursor\n"

// composeProjectManifestCursorClaude opts in with BOTH cursor and claude as
// targets — the shape that exposes empty-Targets over-delivery of composed
// user rules into claude's output.
const composeProjectManifestCursorClaude = "version: 1\n" +
	"canonical:\n" +
	"  local_dir: .agents\n" +
	"targets:\n" +
	"  - cursor\n" +
	"  - claude\n" +
	"compose:\n" +
	"  cursor-rules-from-user: true\n"

func claudeRulePath(base, id string) string {
	return filepath.Join(base, ".claude", "rules", "agent-sync", id+".md")
}

// composeTree builds home/repo with a cursor-targeted project (git root) and a
// user manifest at home. Caller writes the rule files and the two manifests.
// Returns (home, repo).
func composeTree(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, repo
}

func rulePath(base, id string) string {
	return filepath.Join(base, ".cursor", "rules", "agent-sync", id+".mdc")
}

// runInTree runs an arbitrary command from cwd with the home seam swapped, so
// the single-scope path (validate, sync --workspace) resolves the test's user
// scope for composition. Mirrors runSyncHierarchy but for any subcommand.
func runInTree(t *testing.T, cwd, home string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(cwd)
	prev := resolveHome
	resolveHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { resolveHome = prev })

	var out, errBuf bytes.Buffer
	root := NewRootCommand(RootDeps{Out: &out, Err: &errBuf, Version: "test"})
	root.SetArgs(append([]string{args[0], "--non-interactive"}, args[1:]...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

// TestCompose_FoldsUserRulesIntoProject is the U4 happy path: an opted-in
// cursor project folds the user's rule layer into its own .cursor/rules/, so
// user rules + project rules coexist under the project ledger.
func TestCompose_FoldsUserRulesIntoProject(t *testing.T) {
	home, repo := composeTree(t)
	// User rules a, b.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	writeWS(t, home, ".agents/rules/b.md", "user rule b\n")
	// Project rule c, opted in.
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	// All three rules land in the PROJECT's .cursor/rules/agent-sync/.
	for _, id := range []string{"a", "b", "c"} {
		mustExist(t, rulePath(repo, id))
	}
	// User rules were composed in — their bodies appear in the project tree.
	if got, _ := os.ReadFile(rulePath(repo, "a")); !bytes.Contains(got, []byte("user rule a")) {
		t.Errorf("composed rule a missing user body:\n%s", got)
	}
	// Nothing was written under the user's home .cursor (project root is the
	// only write target).
	mustNotExist(t, filepath.Join(home, ".cursor", "rules", "agent-sync"))
}

// TestCompose_ProjectRuleShadowsUserRuleWithWarning is U4/D3: on an id
// collision the project rule wins and the drop is surfaced as a warning.
func TestCompose_ProjectRuleShadowsUserRuleWithWarning(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/shared.md", "USER body for shared\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/shared.md", "PROJECT body for shared\n")

	out, errOut, err := runSyncHierarchy(t, repo, home)
	if err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	// Exactly one shared.mdc, carrying the PROJECT body (project wins).
	got, rerr := os.ReadFile(rulePath(repo, "shared"))
	if rerr != nil {
		t.Fatalf("read shared.mdc: %v", rerr)
	}
	if !bytes.Contains(got, []byte("PROJECT body for shared")) {
		t.Errorf("shared.mdc should carry the project body, got:\n%s", got)
	}
	if bytes.Contains(got, []byte("USER body for shared")) {
		t.Errorf("shared.mdc must not carry the shadowed user body:\n%s", got)
	}
	// The shadow is observable: a warning names the dropped id. The warning goes
	// to the logger (stderr in the command path).
	combined := out + errOut
	if !bytes.Contains([]byte(combined), []byte("shadowed")) || !bytes.Contains([]byte(combined), []byte("shared")) {
		t.Errorf("expected a shadow warning naming id 'shared'; stdout+stderr:\n%s", combined)
	}
}

// TestCompose_NoOptInDoesNotCompose confirms composition is off by default: a
// cursor project WITHOUT the opt-in gets none of the user's rules.
func TestCompose_NoOptInDoesNotCompose(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeCursorNoOptIn), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "c"))    // project rule present
	mustNotExist(t, rulePath(repo, "a")) // user rule NOT composed
}

// TestCompose_NoUserManifestIsNoOp: opt-in set but no user manifest → the
// project syncs its own rules with no error.
func TestCompose_NoUserManifestIsNoOp(t *testing.T) {
	home, repo := composeTree(t) // no user manifest written at home
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "c"))
}

// TestCompose_MalformedUserManifestDefersCursor is U4/D8: a broken user manifest
// must not FAIL the project sync, but composition can't compute the full cursor
// rule set, so the whole cursor sync is DEFERRED this run (its owned subdir is
// left untouched) with a warning. The run still succeeds; other targets (if any)
// are unaffected. This is the accepted tradeoff for the transient-failure guard:
// a broken user source postpones cursor rules rather than syncing a partial set.
func TestCompose_MalformedUserManifestDefersCursor(t *testing.T) {
	home, repo := composeTree(t)
	// Malformed YAML at the user scope. Discovery keys off presence, not
	// validity, so the user scope is discovered; composeUserRules' LoadFile fails.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(":\n  not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	out, errOut, err := runSyncHierarchy(t, repo, home)
	if err != nil {
		t.Fatalf("malformed user manifest must not fail the project sync: %v\nstderr: %s", err, errOut)
	}
	// Cursor deferred → its subdir was not synced this run.
	mustNotExist(t, rulePath(repo, "c"))
	if combined := out + errOut; !bytes.Contains([]byte(combined), []byte("deferring cursor")) {
		t.Errorf("expected a 'deferring cursor' warning; stdout+stderr:\n%s", combined)
	}

	// Recovery: a valid user manifest → next sync writes both project and user
	// rules to cursor.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/u.md", "user rule u\n")
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("recovery sync: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "c"))
	mustExist(t, rulePath(repo, "u"))
}

// TestCompose_TransientUserFailurePreservesComposedRules is the core
// transient-failure guard: after a successful compose writes a user rule into
// the project, a LATER sync that cannot read the user source must NOT wipe the
// previously-composed rule. Deferring the cursor sync leaves its owned subdir
// (composed + project rules) byte-intact; recovery re-syncs it fully.
func TestCompose_TransientUserFailurePreservesComposedRules(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	// Sync 1: composes user rule a alongside project rule c.
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 1: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a"))
	mustExist(t, rulePath(repo, "c"))
	aBefore, rerr := os.ReadFile(rulePath(repo, "a"))
	if rerr != nil {
		t.Fatal(rerr)
	}

	// Break the user source, then sync: cursor deferred → BOTH the composed rule
	// a and the project rule c are preserved byte-intact (subdir untouched).
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(":\n  not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 2 (transient failure): %v\nstderr: %s", err, errOut)
	}
	aAfter, rerr := os.ReadFile(rulePath(repo, "a"))
	if rerr != nil {
		t.Fatalf("composed rule a must survive a deferred sync: %v", rerr)
	}
	if !bytes.Equal(aBefore, aAfter) {
		t.Errorf("composed rule a changed on a deferred sync:\nbefore:\n%s\nafter:\n%s", aBefore, aAfter)
	}
	mustExist(t, rulePath(repo, "c"))

	// Recovery: rule still present after a good sync.
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 3 (recovery): %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a"))
}

// TestCompose_OrphanReclaimOnDropAndOptOut is U4 integration: composed rules are
// project-ledger-owned, so (1) dropping a user rule and (2) opting out both
// reclaim the composed file on the next sync.
func TestCompose_OrphanReclaimOnDropAndOptOut(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	// Sync 1: composes user rule a into the project.
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 1: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a"))

	// Drop user rule a; re-sync → a.mdc reclaimed, c.mdc remains.
	if err := os.Remove(filepath.Join(home, ".agents", "rules", "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 2 (drop rule): %v\nstderr: %s", err, errOut)
	}
	mustNotExist(t, rulePath(repo, "a"))
	mustExist(t, rulePath(repo, "c"))

	// Re-add the rule and sync (present again), then opt out → reclaimed.
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 3 (re-add): %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a"))
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeCursorNoOptIn), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 4 (opt out): %v\nstderr: %s", err, errOut)
	}
	mustNotExist(t, rulePath(repo, "a")) // opt-out reclaimed the composed file
	mustExist(t, rulePath(repo, "c"))    // project's own rule survives
}

// TestCompose_ValidateNoFalseDriftForComposedProject is the regression guard for
// the P1 review finding: validate uses the single-scope path, so before the fix
// it built desired output from project rules only and reported the composed
// .cursor/rules/agent-sync/<id>.mdc as WouldDelete drift — breaking CI/git-hook
// drift guards for every composed project. After the fix, validate composes too
// and reports no drift on a cleanly-composed workspace.
func TestCompose_ValidateNoFalseDriftForComposedProject(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	// Compose via a normal sync, then validate: no drift (exit 0).
	if _, errOut, err := runInTree(t, repo, home, "sync"); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a"))
	out, errOut, err := runInTree(t, repo, home, "validate")
	if err != nil {
		t.Fatalf("validate reported drift on a cleanly-composed project (exit %d): %v\nstdout: %s\nstderr: %s",
			MapExit(err), err, out, errOut)
	}
}

// TestCompose_SingleScopeSyncPreservesComposedRules is the regression guard for
// the P2 review finding: the single-scope path (sync --workspace; watch shares
// it via runWatchSync→prepareEngine) synced without composition and its
// owned-subdir swap deleted previously-composed rules. After the fix it composes
// too, so composed rules survive.
func TestCompose_SingleScopeSyncPreservesComposedRules(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n")

	// Compose via a normal sync.
	if _, errOut, err := runInTree(t, repo, home, "sync"); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a"))

	// A single-scope sync (--workspace) must NOT wipe the composed rule.
	if _, errOut, err := runInTree(t, repo, home, "sync", "--workspace", repo); err != nil {
		t.Fatalf("sync --workspace: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "a")) // preserved: single-scope path composes now
	mustExist(t, rulePath(repo, "c"))
}

// TestCompose_ComposedNodesCarryUserSourceOverride is the U2 per-node
// provenance guard for the plan's composed-provenance open question: nodes
// composed from the USER scope's canonical source must carry their own
// SourceURL/SourceCommit override (here: the user local_dir path and an
// empty commit — a working-tree source has no pin), while the project's
// native nodes keep empty overrides and inherit the session-level source.
func TestCompose_ComposedNodesCarryUserSourceOverride(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.LoadFile(filepath.Join(repo, ".agent-sync.yaml"), manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("load project manifest: %v", err)
	}
	req := engine.Request{
		Targets: []string{cursorTarget},
		Nodes:   []ir.Node{{ID: "c", Kind: ir.KindRule, Version: 1, Body: []byte("project rule c\n")}},
	}
	rc := newTestRuntime()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	user, ok := hierarchy.UserScope(home)
	if !applyCursorComposition(context.Background(), rc, &req, m, "project", user, ok, now) {
		t.Fatal("composition did not fire")
	}

	var sawComposed, sawProject bool
	for _, n := range req.Nodes {
		switch n.ID {
		case "a":
			sawComposed = true
			if n.SourceURL != ".agents" {
				t.Errorf("composed node SourceURL = %q, want user source path %q", n.SourceURL, ".agents")
			}
			if n.SourceCommit != "" {
				t.Errorf("composed node SourceCommit = %q, want empty (local_dir user source has no pin)", n.SourceCommit)
			}
		case "c":
			sawProject = true
			if n.SourceURL != "" || n.SourceCommit != "" {
				t.Errorf("native project node must keep empty source override, got url=%q commit=%q", n.SourceURL, n.SourceCommit)
			}
		}
	}
	if !sawComposed || !sawProject {
		t.Fatalf("expected both composed and project nodes, got %+v", req.Nodes)
	}
}

// TestCompose_Idempotent: two consecutive composed syncs leave the composed
// file byte-identical and produce no error.
func TestCompose_Idempotent(t *testing.T) {
	home, repo := composeTree(t)
	if err := os.WriteFile(filepath.Join(home, ".agent-sync.yaml"), []byte(composeUserManifestCursor), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, home, ".agents/rules/a.md", "user rule a\n")
	if err := os.WriteFile(filepath.Join(repo, ".agent-sync.yaml"), []byte(composeProjectManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, repo, ".agents/rules/c.md", "project rule c\n") // project needs its own canonical dir

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 1: %v\nstderr: %s", err, errOut)
	}
	before, err := os.ReadFile(rulePath(repo, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("sync 2: %v\nstderr: %s", err, errOut)
	}
	after, err := os.ReadFile(rulePath(repo, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("composed rule changed across idempotent syncs:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
