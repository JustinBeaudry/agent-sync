package fsroot

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

// StagedWrite atomically writes data to relPath inside the root.
//
// The write proceeds as: create a uniquely named sibling temp file with
// O_CREATE|O_WRONLY|O_EXCL|O_NOFOLLOW; write data; fsync the file; close;
// rename the temp file over relPath; best-effort fsync the parent
// directory.
//
// All operations are scoped to the root via [os.Root], so:
//   - relPath cannot escape the root (ValidateRelPath + os.Root).
//   - The temp file is always a sibling of the final target, so the
//     rename is by construction intra-directory and therefore
//     cross-filesystem is structurally impossible under normal
//     configurations. We still translate [EXDEV] / [ERROR_NOT_SAME_DEVICE]
//     into [ErrCrossVolume] as defense-in-depth against exotic bind
//     mounts or mount points at the directory level.
//
// StagedWrite does not create missing parent directories. Callers are
// responsible for ensuring the parent exists; the sync pipeline (unit 13)
// creates the staging tree top-down before any StagedWrite calls.
//
// If any step fails, StagedWrite best-effort removes the temp file and
// returns the first error encountered. Partial writes are never visible
// at relPath.
func (r *Root) StagedWrite(relPath string, data []byte, mode fs.FileMode) (retErr error) {
	if err := ValidateRelPath(relPath); err != nil {
		return err
	}
	if err := r.DetectReparsePoint(relPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	dir, base := path.Split(relPath)
	tempName, err := uniqueTempName(base)
	if err != nil {
		return fmt.Errorf("fsroot: generate temp name for %q: %w", relPath, err)
	}
	tempRel := dir + tempName

	f, err := r.inner.OpenFile(tempRel, os.O_CREATE|os.O_WRONLY|os.O_EXCL|oNoFollow, mode)
	if err != nil {
		return fmt.Errorf("fsroot: create staged temp %q: %w", tempRel, translateErr(err))
	}

	cleanup := func() {
		if retErr != nil {
			_ = r.inner.Remove(tempRel)
		}
	}
	defer cleanup()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsroot: write %q: %w", tempRel, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		// Some filesystems (tmpfs on some kernels, specific NFS mounts)
		// legitimately refuse fsync with EINVAL. We do not currently
		// whitelist those; agent-sync writes into user workspaces that are
		// expected to be on durable local filesystems. If this becomes
		// a real portability issue, gate here on errors.Is(err, syscall.EINVAL).
		return fmt.Errorf("fsroot: fsync %q: %w", tempRel, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fsroot: close %q: %w", tempRel, err)
	}

	if err := r.inner.Rename(tempRel, relPath); err != nil {
		return fmt.Errorf("fsroot: rename %q -> %q: %w", tempRel, relPath, translateRenameErr(err))
	}

	// Parent fsync is best-effort. Windows and many network filesystems
	// refuse to fsync a directory handle; we treat that as non-fatal
	// because the file data itself is already durable.
	_ = r.fsyncParent(dir)

	return nil
}

// fsyncParent opens the parent directory inside the root and calls Sync
// on it. Errors are returned to the caller but are treated as non-fatal
// by [Root.StagedWrite].
func (r *Root) fsyncParent(dir string) error {
	// dir may be "" (target at root) or ".../" from path.Split — normalize
	// to an os.Root-openable form.
	d := strings.TrimSuffix(dir, "/")
	if d == "" {
		d = "."
	}
	df, err := r.inner.Open(d)
	if err != nil {
		return err
	}
	defer func() { _ = df.Close() }()
	return df.Sync()
}

// uniqueTempName returns a deterministic-format but cryptographically
// unique temp file name for a staged write. The leading "." keeps the
// temp file out of default shell globs; the "aienv-stage" token lets
// agent-sync identify its own orphaned temp files during crash recovery.
func uniqueTempName(base string) (string, error) {
	var randBuf [8]byte
	if _, err := rand.Read(randBuf[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf(".%s.aienv-stage.%s.tmp", base, hex.EncodeToString(randBuf[:])), nil
}

// translateRenameErr maps EXDEV / ERROR_NOT_SAME_DEVICE to
// [ErrCrossVolume] and leaves other errors alone. Os-specific code in
// samefs_*.go fills this out; here we provide the portable fallback
// that recognizes the stdlib-level signal.
func translateRenameErr(err error) error {
	if err == nil {
		return nil
	}
	if isCrossDevice(err) {
		return fmt.Errorf("%w: %w", ErrCrossVolume, err)
	}
	return translateErr(err)
}
