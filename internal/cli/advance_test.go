package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/manifest"
)

func TestResolveAdvance_FastForward(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	newSHA := commitFile(t, canonical, "rules/more.md", "More rules.\n", "second rule")
	ws := writeUpdateWS(t, canonical, head, "main")

	m, err := manifest.LoadFile(filepath.Join(ws, ".agent-sync.yaml"), manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	res, err := resolveAdvance(context.Background(), m)
	if err != nil {
		t.Fatalf("resolveAdvance: %v", err)
	}
	if res.newSHA != newSHA {
		t.Fatalf("newSHA = %q, want %q", res.newSHA, newSHA)
	}
	if !res.fastForward {
		t.Fatal("fastForward = false, want true")
	}
}

func TestResolveAdvance_RewrittenHistory(t *testing.T) {
	requireGit(t)
	canonical, rootSHA, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "main")
	mustGit(t, canonical, "reset", "--hard", rootSHA)
	rewritten := commitFile(t, canonical, "rules/rewritten.md", "Rewritten.\n", "rewritten history")

	m, err := manifest.LoadFile(filepath.Join(ws, ".agent-sync.yaml"), manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	res, err := resolveAdvance(context.Background(), m)
	if err != nil {
		t.Fatalf("resolveAdvance: %v", err)
	}
	if res.newSHA != rewritten {
		t.Fatalf("newSHA = %q, want %q", res.newSHA, rewritten)
	}
	if res.fastForward {
		t.Fatal("fastForward = true, want false")
	}
}

func TestResolveAdvance_MissingRefFollowsHEAD(t *testing.T) {
	requireGit(t)
	canonical, _, head := makeUpdateRepo(t)
	ws := writeUpdateWS(t, canonical, head, "")
	newSHA := commitFile(t, canonical, "rules/more.md", "More.\n", "second")

	m, err := manifest.LoadFile(filepath.Join(ws, ".agent-sync.yaml"), manifest.LoadOptions{NonInteractive: true})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	res, err := resolveAdvance(context.Background(), m)
	if err != nil {
		t.Fatalf("resolveAdvance: %v", err)
	}
	if res.newSHA != newSHA {
		t.Fatalf("newSHA = %q, want %q", res.newSHA, newSHA)
	}
	if !res.refFromHEAD {
		t.Fatal("refFromHEAD = false, want true")
	}
}

func TestRepinManifest_PreservesCommentsAndUpdatesBothSHAs(t *testing.T) {
	ws := t.TempDir()
	manifestPath := filepath.Join(ws, ".agent-sync.yaml")
	src := strings.Join([]string{
		"# top comment",
		"version: 1",
		"canonical:",
		"  # ref comment",
		"  local_path: /tmp/example",
		"  ref: main",
		"  commit: 1111111111111111111111111111111111111111",
		"trusted_sha: 1111111111111111111111111111111111111111",
		"targets:",
		"  - claude",
		"",
	}, "\n")
	if err := os.WriteFile(manifestPath, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	const newSHA = "2222222222222222222222222222222222222222"
	if err := repinManifest(manifestPath, newSHA); err != nil {
		t.Fatalf("repinManifest: %v", err)
	}

	gotBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"# top comment",
		"  # ref comment",
		"  commit: " + newSHA,
		"trusted_sha: " + newSHA,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("manifest missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "  commit: "+newSHA) > strings.Index(got, "trusted_sha: "+newSHA) {
		t.Fatalf("commit moved after trusted_sha:\n%s", got)
	}
}
