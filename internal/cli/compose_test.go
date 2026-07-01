package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// composeProjectManifest is a project manifest targeting cursor with the
// hierarchy-composition opt-in enabled.
const composeProjectManifest = "version: 1\n" +
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

// TestCompose_MalformedUserManifestIsSoftNoOp is U4/D8: a broken user manifest
// must not fail the project sync — composition soft-no-ops with a warning and
// the project still writes its own rules.
func TestCompose_MalformedUserManifestIsSoftNoOp(t *testing.T) {
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

	if _, errOut, err := runSyncHierarchy(t, repo, home); err != nil {
		t.Fatalf("malformed user manifest must not fail project sync: %v\nstderr: %s", err, errOut)
	}
	mustExist(t, rulePath(repo, "c"))
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
