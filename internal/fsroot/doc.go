// Package fsroot provides the safe-filesystem primitives that every other
// aienvs package must use to read and write paths inside a user workspace.
//
// The package wraps Go 1.25's [os.Root] with four capabilities that the rest
// of aienvs depends on:
//
//  1. Containment. All operations are scoped to a single directory handle
//     and cannot escape it through symlinks, "..", absolute paths, or
//     Windows device namespaces. This is enforced by [os.Root] itself.
//
//  2. Atomic writes via [Root.StagedWrite]. Writes go through a uniquely
//     named sibling temp file, fsync the file (and best-effort fsync the
//     parent directory), then rename atomically. The temp file is cleaned
//     up on every error path.
//
//  3. Advisory cross-filesystem detection via [SameFilesystem]. The
//     authoritative cross-FS signal is the error returned by
//     [os.Root.Rename] (EXDEV / ERROR_NOT_SAME_DEVICE); [SameFilesystem] is
//     only used to enrich error messages, never to gate operations. See
//     plan decision #20.
//
//  4. Irregular-file refusal via [Root.DetectReparsePoint]. Reparse points,
//     device files, sockets, and named pipes surface as
//     [fs.ModeIrregular] after [os.Root]'s own filtering. aienvs refuses
//     to write through them as defense-in-depth.
//
// # Scoping
//
// Per plan decision #6, callers typically open a [Root] at the parent of
// the target's reserved prefix (e.g. "<workspace>/.claude/") rather than
// at the reserved prefix itself. This lets a single [Root] scope both
// legs of the atomic two-rename swap in unit 13 without the root handle
// blocking its own rename on Windows.
//
// # Non-goals
//
// This package intentionally does not abstract the filesystem behind an
// interface. Tests that need a fake filesystem should either operate
// against a real [testing.T.TempDir] (preferred for anything touching
// rename semantics) or sit at a higher layer that uses fsroot only
// through its exported functions.
package fsroot
