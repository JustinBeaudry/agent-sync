package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLocalDirWorkspace builds a workspace whose canonical source is an
// in-repo .agents directory targeting claude + codex, with one authored skill
// and one authored rule. No git is involved.
func writeLocalDirWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	manifest := "version: 1\n" +
		"canonical:\n" +
		"  local_dir: .agents\n" +
		"targets:\n" +
		"  - claude\n" +
		"  - codex\n"
	if err := os.WriteFile(filepath.Join(ws, ".agent-sync.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWS(t, ws, ".agents/skills/foo/SKILL.md", "authored skill body\n")
	writeWS(t, ws, ".agents/rules/r.md", "No deploys on Friday.\n")
	return ws
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat err = %v", path, err)
	}
}

// Full path: an in-repo source compiles to both tools, offline, with no git
// and no pin; emitted skills coexist with the authored source in .agents/skills.
func TestSync_LocalDir_EndToEndOffline(t *testing.T) {
	ws := writeLocalDirWorkspace(t)

	// --offline proves the local_dir path is exempt from the network/pin gate.
	if _, errOut, err := runSync(t, ws, "--offline"); err != nil {
		t.Fatalf("sync failed: %v\nstderr: %s", err, errOut)
	}

	// Claude outputs.
	mustExist(t, filepath.Join(ws, ".claude", "skills", "agent-sync-foo", "SKILL.md"))
	mustExist(t, filepath.Join(ws, ".claude", "rules", "agent-sync", "r.md"))
	// Codex emits skills into the shared .agents/skills tree under the
	// agent-sync- prefix.
	mustExist(t, filepath.Join(ws, ".agents", "skills", "agent-sync-foo", "SKILL.md"))
	// Coexistence: the authored source skill is untouched by the emitted output.
	mustExist(t, filepath.Join(ws, ".agents", "skills", "foo", "SKILL.md"))
}

// A second sync with no source change must be a no-op, even though the first
// sync wrote agent-sync-foo into the same .agents/skills tree the reader scans.
func TestSync_LocalDir_IdempotentAcrossSharedTree(t *testing.T) {
	ws := writeLocalDirWorkspace(t)
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("first sync: %v\n%s", err, errOut)
	}
	out, errOut, err := runSync(t, ws, "--output", "json")
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "\"unchanged\"") {
		t.Fatalf("second sync should report an unchanged target:\n%s", out)
	}
	// The authored source skill must still be present after re-sync.
	mustExist(t, filepath.Join(ws, ".agents", "skills", "foo", "SKILL.md"))
}

// Removing an authored source file orphans its emitted output, which the next
// sync deletes — while leaving the authored source and other outputs intact.
func TestSync_LocalDir_OrphanDeletion(t *testing.T) {
	ws := writeLocalDirWorkspace(t)
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("first sync: %v\n%s", err, errOut)
	}
	ruleOut := filepath.Join(ws, ".claude", "rules", "agent-sync", "r.md")
	mustExist(t, ruleOut)

	// Delete the authored rule and re-sync.
	if err := os.Remove(filepath.Join(ws, ".agents", "rules", "r.md")); err != nil {
		t.Fatalf("remove authored rule: %v", err)
	}
	if _, errOut, err := runSync(t, ws); err != nil {
		t.Fatalf("re-sync: %v\n%s", err, errOut)
	}
	mustNotExist(t, ruleOut)
	// The skill outputs and authored source remain.
	mustExist(t, filepath.Join(ws, ".claude", "skills", "agent-sync-foo", "SKILL.md"))
	mustExist(t, filepath.Join(ws, ".agents", "skills", "foo", "SKILL.md"))
}
