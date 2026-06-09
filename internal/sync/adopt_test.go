package sync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfirmAdopt_RequiresTypedTargetName(t *testing.T) {
	t.Parallel()
	if !ConfirmAdopt("claude", "claude") {
		t.Error("exact target name should confirm")
	}
	for _, typed := range []string{"y", "yes", "Claude", "", "cursor"} {
		if ConfirmAdopt(typed, "claude") {
			t.Errorf("typed %q must NOT confirm adoption of claude", typed)
		}
	}
}

func TestBackupRel_Shape(t *testing.T) {
	t.Parallel()
	got := BackupRel("claude", "20260608T010203Z")
	want := ".aienv/state/backups/claude-20260608T010203Z.tar.gz"
	if got != want {
		t.Errorf("BackupRel = %s want %s", got, want)
	}
}

func seedPrefix(t *testing.T, ws string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(ws, testPrefix, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestBackup_ContentsMatchPrefix(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	files := map[string]string{"a.md": "AAA", "sub/b.md": "BBB"}
	seedPrefix(t, ws, files)

	dest := BackupRel("claude", "20260608T010203Z")
	if err := Backup(root, testPrefix, dest, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Extract and compare contents (names are prefix-relative).
	raw, err := os.ReadFile(filepath.Join(ws, dest))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatalf("tar next: %v", e)
		}
		b, _ := io.ReadAll(tr)
		got[hdr.Name] = string(b)
	}
	if len(got) != len(files) {
		t.Fatalf("backup has %d entries want %d: %v", len(got), len(files), got)
	}
	for name, content := range files {
		if got[name] != content {
			t.Errorf("backup[%s] = %q want %q", name, got[name], content)
		}
	}
}

func TestAdoptEntries_HashesExistingFiles(t *testing.T) {
	t.Parallel()
	root, ws := newWS(t)
	seedPrefix(t, ws, map[string]string{"a.md": "AAA"})
	now := time.Unix(100, 0).UTC()

	entries, err := AdoptEntries(root, testPrefix, now)
	if err != nil {
		t.Fatalf("AdoptEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries want 1", len(entries))
	}
	e := entries[0]
	if e.Path != testPrefix+"/a.md" {
		t.Errorf("path = %s", e.Path)
	}
	sum := sha256.Sum256([]byte("AAA"))
	if e.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("hash mismatch")
	}
	if e.Size != 3 || !e.EmittedAt.Equal(now) {
		t.Errorf("size/time wrong: size=%d at=%v", e.Size, e.EmittedAt)
	}
}
