package merge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/locks"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// ApplyToFile is the one filesystem-touching entry point: it holds the
// Unit 12 per-external-file flock across the whole read-merge-write,
// dispatches to the pure engine by locator kind, and writes the result
// atomically via fsroot (temp+fsync+rename). On any engine error it
// returns fail-closed — the on-disk file is left byte-identical.
//
// relPath is workspace-relative (forward-slash). holder is the adapter
// name, used in the per-file lock's timeout diagnostics.
func ApplyToFile(ctx context.Context, root *fsroot.Root, reg *locks.FileLockRegistry, relPath string, e MergeEntry, holder string) (sliceHash, warning string, err error) {
	abs := filepath.Join(root.Path(), filepath.FromSlash(relPath))
	release, err := reg.Acquire(ctx, abs, holder, locks.FileLockOpts{})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = release() }()

	existing, err := readExisting(root, relPath)
	if err != nil {
		return "", "", err
	}

	merged, sliceHash, warning, err := mergeDispatch(existing, e)
	if err != nil {
		return "", "", err // fail-closed: nothing written
	}

	// StagedWrite does not create parents; a nested target (.cursor/,
	// .codex/) on first sync needs the dir created first.
	if dir := slashDir(relPath); dir != "" {
		if mkErr := root.Inner().MkdirAll(dir, 0o755); mkErr != nil {
			return "", "", fmt.Errorf("merge: mkdir %s: %w", dir, mkErr)
		}
	}
	if werr := root.StagedWrite(relPath, merged, 0o644); werr != nil {
		return "", "", fmt.Errorf("merge: write %s: %w", relPath, werr)
	}
	return sliceHash, warning, nil
}

// mergeDispatch routes to the pure per-kind merge engine and is the single
// source of truth shared by ApplyToFile (write) and DryMerge (validate). A
// shared dispatch guarantees the bytes the plan path predicts are computed by
// the exact same code the sync path writes — they cannot drift apart.
func mergeDispatch(existing []byte, e MergeEntry) (merged []byte, sliceHash, warning string, err error) {
	switch e.Kind {
	case adapterkit.ToolOwnedKindJSONPointer:
		merged, sliceHash, err = mergeJSON(existing, e)
	case adapterkit.ToolOwnedKindTOMLPath:
		merged, sliceHash, err = mergeTOML(existing, e)
	case adapterkit.ToolOwnedKindMarkdownSection:
		merged, sliceHash, warning, err = mergeMarkdown(existing, e)
	default:
		return nil, "", "", fmt.Errorf("merge: unknown locator kind %q", e.Kind)
	}
	return merged, sliceHash, warning, err
}

// DryMerge reports what ApplyToFile would do to relPath WITHOUT taking the
// write lock or mutating anything. It runs the shared merge engine against the
// current on-disk file and compares the merged bytes byte-for-byte.
//
// This is the drift signal for tool-owned files — it answers "would a sync
// rewrite this file?". Because the merge is idempotent (re-applying the same
// content to already-merged output yields byte-identical bytes), a clean sync
// followed by DryMerge reports changed=false. It also catches out-of-band
// edits inside the managed slice (the merge re-renders the slice, so merged
// differs from the edited file) while leaving user content outside the slice
// untouched (merged equals existing there). Errors mirror ApplyToFile
// (malformed marker/JSON/TOML state, unknown kind) so validate surfaces the
// same failures a real sync would hit.
func DryMerge(root *fsroot.Root, relPath string, e MergeEntry) (exists, changed bool, warning string, err error) {
	existing, exists, err := readExistingForDry(root, relPath)
	if err != nil {
		return false, false, "", err
	}
	merged, _, warning, err := mergeDispatch(existing, e)
	if err != nil {
		return exists, false, "", err
	}
	return exists, !bytes.Equal(merged, existing), warning, nil
}

func readExisting(root *fsroot.Root, relPath string) ([]byte, error) {
	data, _, err := readExistingForDry(root, relPath)
	return data, err
}

// readExistingForDry reads relPath and reports whether it exists. A missing
// file returns (nil, false, nil); an empty file returns (nil/[]byte{}, true,
// nil), so callers can distinguish create from update.
func readExistingForDry(root *fsroot.Root, relPath string) (data []byte, exists bool, err error) {
	f, err := root.Inner().Open(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("merge: read %s: %w", relPath, err)
	}
	defer func() { _ = f.Close() }()
	data, err = io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// slashDir returns the parent directory of a forward-slash relative
// path, or "" when the path is at the workspace root.
func slashDir(relPath string) string {
	i := strings.LastIndex(relPath, "/")
	if i <= 0 {
		return ""
	}
	return relPath[:i]
}
