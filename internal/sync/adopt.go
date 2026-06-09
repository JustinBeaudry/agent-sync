package sync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/ledger"
)

// backupsDir is where adopt-prefix backup tarballs are written.
const backupsDir = ".aienv/state/backups"

// ConfirmAdopt reports whether the user's typed input authorizes
// adopting target. Adoption requires typing the exact target name (e.g.
// "claude") — a bare "y"/"yes" is intentionally NOT sufficient, mirroring
// `gh repo delete`'s typed-name confirmation for a destructive action.
func ConfirmAdopt(typed, target string) bool {
	return typed == target
}

// BackupRel returns the workspace-relative path for a target's adopt
// backup tarball at the given instant (caller stamps the timestamp).
func BackupRel(target, timestamp string) string {
	return path.Join(backupsDir, target+"-"+timestamp+".tar.gz")
}

// Backup writes a .tar.gz of the entire reserved prefix to destRel.
// Entry names are relative to the prefix so the archive restores cleanly
// over the prefix. The caller prints destRel prominently before any
// destructive adoption follows.
func Backup(root *fsroot.Root, prefixRel, destRel string, now time.Time) error {
	files, err := walkFiles(root, prefixRel)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		b, rerr := readFile(root, f)
		if rerr != nil {
			return rerr
		}
		name := strings.TrimPrefix(f, prefixRel+"/")
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: now}
		if werr := tw.WriteHeader(hdr); werr != nil {
			return fmt.Errorf("sync: backup header %s: %w", name, werr)
		}
		if _, werr := tw.Write(b); werr != nil {
			return fmt.Errorf("sync: backup write %s: %w", name, werr)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("sync: backup tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("sync: backup gzip close: %w", err)
	}
	if err := root.Inner().MkdirAll(path.Dir(destRel), 0o755); err != nil {
		return fmt.Errorf("sync: backup mkdir: %w", err)
	}
	if err := root.StagedWrite(destRel, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("sync: backup write %s: %w", destRel, err)
	}
	return nil
}

// AdoptEntries records every existing file under the reserved prefix as
// a ledger entry "as-adopted" (hash of its current bytes). After
// adoption a normal sync applies orphan deletion: adopted files that the
// new generation does not re-emit are deleted (the backup preserves
// them). Caller stamps now.
func AdoptEntries(root *fsroot.Root, prefixRel string, now time.Time) ([]ledger.Entry, error) {
	files, err := walkFiles(root, prefixRel)
	if err != nil {
		return nil, err
	}
	entries := make([]ledger.Entry, 0, len(files))
	for _, f := range files {
		b, rerr := readFile(root, f)
		if rerr != nil {
			return nil, rerr
		}
		sum := sha256.Sum256(b)
		entries = append(entries, ledger.Entry{
			Path:      f,
			SHA256:    hex.EncodeToString(sum[:]),
			Size:      int64(len(b)),
			EmittedAt: now,
		})
	}
	return entries, nil
}
