package merge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/locks"
)

type NativeKind string

const (
	NativeKindTOMLKey       NativeKind = "toml-key"
	NativeKindGeneratedJSON NativeKind = "generated-json"
)

type NativeEntry struct {
	Kind    NativeKind
	Locator string
	Content []byte
}

func mergeNative(existing []byte, entries []NativeEntry) ([]byte, string, error) {
	out := append([]byte(nil), existing...)
	for _, e := range entries {
		var err error
		switch e.Kind {
		case NativeKindTOMLKey:
			out, err = mergeNativeTOMLKey(out, e)
		case NativeKindGeneratedJSON:
			out, err = mergeNativeGeneratedJSON(out, e)
		default:
			return nil, "", fmt.Errorf("merge: unknown native kind %q", e.Kind)
		}
		if err != nil {
			return nil, "", err
		}
	}
	h := sha256.Sum256(out)
	return out, hex.EncodeToString(h[:]), nil
}

func ApplyNativeToFile(ctx context.Context, root *fsroot.Root, reg *locks.FileLockRegistry, relPath string, entries []NativeEntry, holder string) (string, error) {
	abs := filepath.Join(root.Path(), filepath.FromSlash(relPath))
	release, err := reg.Acquire(ctx, abs, holder, locks.FileLockOpts{})
	if err != nil {
		return "", err
	}
	defer func() { _ = release() }()

	existing, err := readExisting(root, relPath)
	if err != nil {
		return "", err
	}
	merged, hash, err := mergeNative(existing, entries)
	if err != nil {
		return "", err
	}

	if dir := slashDir(relPath); dir != "" {
		if mkErr := root.Inner().MkdirAll(dir, 0o755); mkErr != nil {
			return "", fmt.Errorf("merge: mkdir %s: %w", dir, mkErr)
		}
	}
	if err := root.StagedWrite(relPath, merged, 0o644); err != nil {
		return "", fmt.Errorf("merge: write native %s: %w", relPath, err)
	}
	return hash, nil
}

func DryNativeMerge(root *fsroot.Root, relPath string, entries []NativeEntry) (exists, changed bool, err error) {
	existing, exists, err := readExistingForDry(root, relPath)
	if err != nil {
		return false, false, err
	}
	merged, _, err := mergeNative(existing, entries)
	if err != nil {
		return exists, false, err
	}
	return exists, !bytes.Equal(merged, existing), nil
}
