package merge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/locks"
)

func applyHarness(t *testing.T) (*fsroot.Root, *locks.FileLockRegistry, string) {
	t.Helper()
	ws := t.TempDir()
	root, err := fsroot.OpenWorkspaceRoot(ws)
	if err != nil {
		t.Fatalf("OpenWorkspaceRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	reg, err := locks.NewFileLockRegistry(root)
	if err != nil {
		t.Fatalf("NewFileLockRegistry: %v", err)
	}
	return root, reg, ws
}

func TestApplyToFile_WritesEachKind(t *testing.T) {
	t.Parallel()
	root, reg, ws := applyHarness(t)
	ctx := context.Background()

	// markdown at workspace root
	if _, _, err := ApplyToFile(ctx, root, reg, "AGENTS.md", mdUpsert("foo", "body\n"), "cursor"); err != nil {
		t.Fatalf("markdown apply: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md")); !bytes.Contains(b, []byte("agent-sync:begin id=foo")) {
		t.Errorf("AGENTS.md not written: %s", b)
	}
	// json at workspace root
	if _, _, err := ApplyToFile(ctx, root, reg, ".mcp.json", jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`), "claude"); err != nil {
		t.Fatalf("json apply: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, ".mcp.json")); !bytes.Contains(b, []byte("aienvs_foo")) {
		t.Errorf(".mcp.json not written: %s", b)
	}
}

func TestApplyToFile_NestedNewTargetCreatesParent(t *testing.T) {
	t.Parallel()
	root, reg, ws := applyHarness(t)
	// .cursor/ does not exist yet; ApplyToFile must MkdirAll it.
	if _, _, err := ApplyToFile(context.Background(), root, reg, ".cursor/mcp.json", jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`), "cursor"); err != nil {
		t.Fatalf("nested apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".cursor", "mcp.json")); err != nil {
		t.Errorf(".cursor/mcp.json not created: %v", err)
	}
}

func TestApplyToFile_EngineErrorLeavesFileUntouched(t *testing.T) {
	t.Parallel()
	root, reg, ws := applyHarness(t)
	// Seed an invalid JSON file.
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := ApplyToFile(context.Background(), root, reg, ".mcp.json", jsonUpsert("/mcpServers/aienvs_foo", `{"command":"node"}`), "claude")
	if !errors.Is(err, ErrMalformedToolOwnedFile) {
		t.Fatalf("err=%v want ErrMalformedToolOwnedFile", err)
	}
	// File must be byte-identical (no write on error).
	if b, _ := os.ReadFile(filepath.Join(ws, ".mcp.json")); string(b) != "{not json" {
		t.Errorf("file changed despite engine error: %s", b)
	}
}

func TestApplyToFile_ConcurrentSameFileSerializes(t *testing.T) {
	t.Parallel()
	root, reg, ws := applyHarness(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, id := range []string{"cursor", "codex"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, _, err := ApplyToFile(ctx, root, reg, "AGENTS.md", mdUpsert(id, "body-"+id+"\n"), id); err != nil {
				t.Errorf("apply %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()
	// Both sections present, file well-formed (no torn write).
	b, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if !bytes.Contains(b, []byte("agent-sync:begin id=cursor")) || !bytes.Contains(b, []byte("agent-sync:begin id=codex")) {
		t.Errorf("concurrent merges lost a section:\n%s", b)
	}
	// Re-parse via the engine to confirm marker integrity.
	if _, _, _, err := mergeMarkdown(b, mdUpsert("cursor", "x\n")); err != nil {
		t.Errorf("merged file has broken markers: %v", err)
	}
}
