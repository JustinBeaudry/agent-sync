package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAdapterManifestFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adapter.yaml")
	src := "name: claude\ncontract_version: aienvs/v1\ncommand: [x]\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadAdapterManifestFile(path)
	if err != nil {
		t.Fatalf("LoadAdapterManifestFile: %v", err)
	}
	if m.Name != "claude" {
		t.Fatalf("name = %q, want claude", m.Name)
	}
	if m.ContractVersion != "aienvs/v1" {
		t.Fatalf("contract_version = %q, want aienvs/v1", m.ContractVersion)
	}
	if len(m.Command) != 1 || m.Command[0] != "x" {
		t.Fatalf("command = %v, want [x]", m.Command)
	}
}

func TestLoadAdapterManifestFile_Missing(t *testing.T) {
	_, err := LoadAdapterManifestFile(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected stat error for missing file")
	}
}

func TestSource_String(t *testing.T) {
	cases := map[Source]string{
		SourceWorkspaceManifest: "workspace-manifest",
		SourcePATH:              "path",
		SourceBundled:           "bundled",
		Source(255):             "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", s, got, want)
		}
	}
}
