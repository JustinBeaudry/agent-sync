package worktree_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/internal/worktree"
)

// writeFile creates parent dirs and writes a file under the workspace root.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func openReader(t *testing.T, ws, localDir string) *worktree.Reader {
	t.Helper()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("open workspace root: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	r, err := worktree.NewReader(root, localDir)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	return r
}

// idsByKind decodes through the reader and groups node IDs by kind.
func idsByKind(t *testing.T, r *worktree.Reader) map[ir.Kind][]string {
	t.Helper()
	nodes, _, err := ir.Decode(r, "", ir.DecodeOptions{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[ir.Kind][]string{}
	for _, n := range nodes {
		out[n.Kind] = append(out[n.Kind], n.ID)
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestReader_DecodesCanonicalLayout(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, ".agents/AGENTS.md", "# Agents\n")
	writeFile(t, ws, ".agents/rules/x.md", "rule body\n")
	writeFile(t, ws, ".agents/skills/foo/SKILL.md", "skill body\n")
	writeFile(t, ws, ".agents/skills/foo/helper.py", "print('hi')\n")
	writeFile(t, ws, ".agents/commands/y.md", "command body\n")
	writeFile(t, ws, ".agents/mcp/z.json", "{\"command\":\"x\"}\n")

	r := openReader(t, ws, ".agents")
	got := idsByKind(t, r)

	if !contains(got[ir.KindRule], "x") {
		t.Errorf("rule x missing; rules=%v", got[ir.KindRule])
	}
	if !contains(got[ir.KindSkill], "foo") {
		t.Errorf("skill foo missing; skills=%v", got[ir.KindSkill])
	}
	if !contains(got[ir.KindCommand], "y") {
		t.Errorf("command y missing; commands=%v", got[ir.KindCommand])
	}
	if !contains(got[ir.KindMCPServerEntry], "z") {
		t.Errorf("mcp z missing; mcp=%v", got[ir.KindMCPServerEntry])
	}
	if len(got[ir.KindAgentsMD]) == 0 {
		t.Errorf("AGENTS.md node missing")
	}

	// Skill assets are gathered relative to local_dir.
	nodes, _, _ := ir.Decode(r, "", ir.DecodeOptions{})
	skills, _ := ir.SkillsByID(nodes, r, "")
	foo, ok := skills["foo"]
	if !ok {
		t.Fatalf("SkillsByID missing foo; got keys %v", keys(skills))
	}
	if len(foo.Assets) != 1 || foo.Assets[0].RelPath != "helper.py" {
		t.Errorf("skill foo assets = %+v, want [helper.py]", foo.Assets)
	}
}

func TestReader_SkipsOwnedOutputSkills(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, ".agents/skills/foo/SKILL.md", "authored skill\n")
	// Simulate a prior emitted output in the shared tree.
	writeFile(t, ws, ".agents/skills/agent-sync-foo/SKILL.md", "emitted output\n")
	writeFile(t, ws, ".agents/skills/agent-sync-bar/SKILL.md", "emitted output\n")

	r := openReader(t, ws, ".agents")
	got := idsByKind(t, r)

	if !contains(got[ir.KindSkill], "foo") {
		t.Errorf("authored skill foo missing; skills=%v", got[ir.KindSkill])
	}
	for _, id := range got[ir.KindSkill] {
		if id == "agent-sync-foo" || id == "agent-sync-bar" {
			t.Errorf("owned output skill %q was read as source; skills=%v", id, got[ir.KindSkill])
		}
	}
}

func TestReader_SkipsSymlinks(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, ".agents/rules/real.md", "real\n")
	link := filepath.Join(ws, ".agents", "rules", "link.md")
	if err := os.Symlink(filepath.Join(ws, ".agents", "rules", "real.md"), link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	r := openReader(t, ws, ".agents")
	entries, err := r.ReadTree("")
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	for _, e := range entries {
		if e.Path == "rules/link.md" {
			t.Errorf("symlink rules/link.md was emitted as a source entry")
		}
	}
}

func TestReader_EmptyDirYieldsNoEntries(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r := openReader(t, ws, ".agents")
	entries, err := r.ReadTree("")
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want none", entries)
	}
	// Decoder turns an empty source into empty IR + the missing-AGENTS warning.
	nodes, warns, err := ir.Decode(r, "", ir.DecodeOptions{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("nodes = %v, want none", nodes)
	}
	if len(warns) == 0 {
		t.Errorf("expected WarnAgentsMDMissing for an empty source")
	}
}

func TestReader_MissingDirIsClearError(t *testing.T) {
	ws := t.TempDir()
	r := openReader(t, ws, ".agents") // never created
	_, err := r.ReadTree("")
	if !errors.Is(err, worktree.ErrSourceMissing) {
		t.Fatalf("ReadTree err = %v, want ErrSourceMissing", err)
	}
}

func TestNewReader_RejectsUnsafeLocalDir(t *testing.T) {
	ws := t.TempDir()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	for _, bad := range []string{"", ".", "../escape", "/abs"} {
		if _, err := worktree.NewReader(root, bad); err == nil {
			t.Errorf("NewReader(%q) = nil error, want rejection", bad)
		}
	}
}

func keys(m map[string]ir.Skill) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
