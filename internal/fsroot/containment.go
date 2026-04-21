package fsroot

import (
	"errors"
	"fmt"
	"io/fs"
)

// DetectReparsePoint reports a non-nil error wrapping [ErrIrregular] if
// the filesystem object at relPath is an "irregular" object — on
// Windows, a non-symlink reparse point (junction, mount point, or other
// reparse tag); on any OS, a device file, socket, or named pipe.
//
// It returns nil if relPath refers to a regular file, directory, or
// symlink (symlinks within the root are an intentional discovery
// pattern per R8, and are constrained by [os.Root] containment).
//
// If relPath does not exist, the returned error wraps [fs.ErrNotExist];
// callers that want to tolerate a missing target (for example
// [Root.StagedWrite] before the file exists) should check with
// [errors.Is].
//
// This is defense-in-depth on top of [os.Root]'s own filtering: [os.Root]
// already refuses to traverse reparse points that would escape the root.
// DetectReparsePoint refuses to write *through* any reparse-point target
// even when it stays within the root, because aienvs treats reparse-
// point write targets as a protocol violation from the adapter.
func (r *Root) DetectReparsePoint(relPath string) error {
	if err := ValidateRelPath(relPath); err != nil {
		return err
	}
	fi, err := r.inner.Lstat(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return translateErr(err)
	}
	mode := fi.Mode()
	// Regular files, directories, and symlinks are allowed.
	if mode.IsRegular() || mode.IsDir() || mode&fs.ModeSymlink != 0 {
		return nil
	}
	if mode&fs.ModeIrregular != 0 {
		return fmt.Errorf("%w: %q mode=%s", ErrIrregular, relPath, mode)
	}
	// Device files, sockets, named pipes, char devices.
	if mode&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0 {
		return fmt.Errorf("%w: %q mode=%s", ErrIrregular, relPath, mode)
	}
	return nil
}
