package engine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sort"
	"time"

	"github.com/aienvs/aienvs/internal/adapter/contract"
	"github.com/aienvs/aienvs/internal/fsroot"
	"github.com/aienvs/aienvs/internal/ledger"
)

// planTarget computes the dry-run change set for one target without
// mutating the workspace. It runs the adapter (read-only) and diffs the
// desired ops against the current ledger and on-disk state.
func planTarget(ctx context.Context, req Request, target string, _ time.Time) TargetChange {
	change := TargetChange{Target: target}

	old, err := loadLedger(req.Root, target)
	if err != nil {
		change.Error = err.Error()
		return change
	}
	oldByPath := map[string]ledger.Entry{}
	for _, e := range old.Entries {
		oldByPath[e.Path] = e
	}

	out, err := runAdapter(ctx, req, target)
	if err != nil {
		change.Error = err.Error()
		return change
	}
	change.Warnings = out.warnings

	desired := map[string]string{} // path -> sha256 (write_file only)
	for _, op := range out.ops {
		switch o := op.(type) {
		case contract.OpWriteFile:
			desired[o.Path] = sha256Hex(o.Content)
		case contract.OpWriteToolOwned:
			// Tool-owned slices: treat presence as an update candidate;
			// a precise slice diff is deferred (v1 merges idempotently).
			if _, known := oldByPath[o.Path]; known {
				change.WouldUpdate = append(change.WouldUpdate, o.Path)
			} else {
				change.WouldCreate = append(change.WouldCreate, o.Path)
			}
		}
	}

	for p, want := range desired {
		prev, known := oldByPath[p]
		switch {
		case !known:
			change.WouldCreate = append(change.WouldCreate, p)
		case prev.SHA256 != want:
			change.WouldUpdate = append(change.WouldUpdate, p)
		default:
			// Ledger says unchanged — check for out-of-band edits on disk.
			if onDisk, ok := readHash(req.Root, p); ok && onDisk != prev.SHA256 {
				change.OutOfBand = append(change.OutOfBand, p)
			}
		}
	}

	// Deletions: ledger paths under an owned subdir no longer desired.
	for _, e := range old.Entries {
		if ownerOf(out.ownedPrefixes, e.Path) == "" {
			continue
		}
		if _, stillWanted := desired[e.Path]; !stillWanted {
			change.WouldDelete = append(change.WouldDelete, e.Path)
		}
	}

	change.WouldCreate = sortedStrings(change.WouldCreate)
	change.WouldUpdate = sortedStrings(change.WouldUpdate)
	change.WouldDelete = sortedStrings(change.WouldDelete)
	change.OutOfBand = sortedStrings(change.OutOfBand)
	sort.Strings(change.Warnings)
	return change
}

// sortedStrings returns a sorted copy of in.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// readHash reads the file at relPath through the root and returns its
// sha256 hex, or ("", false) if it cannot be read (absent/irregular).
func readHash(root *fsroot.Root, relPath string) (string, bool) {
	f, err := root.Inner().Open(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
		return "", false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", false
	}
	return sha256Hex(data), true
}
