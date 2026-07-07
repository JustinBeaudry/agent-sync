package engine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sort"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/harness"
	"github.com/agent-sync/agent-sync/internal/ledger"
	"github.com/agent-sync/agent-sync/internal/merge"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
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

	// Mirror applyTarget: operate on owned-subdir prefixes plus the managed
	// leaf dirs of shared-subdirs (never the shared parent) so deletion
	// accounting covers removed agent-sync leaves without flagging foreign
	// sibling content under a shared tree.
	effective := effectiveOwnedPrefixes(out.ownedPrefixes, out.sharedPrefixes, out.fileLeafParents, out.ops, old.Entries)

	// Cross-adapter co-ownership (ADV-1): mirror applyTarget so validate's
	// WouldDelete matches what sync actually does. A shared-subdir leaf path a
	// sibling target still claims is NOT deleted by sync (the release filter /
	// orphan guard skip it), so it must not be reported as a deletion here —
	// otherwise `validate --target codex` reports false drift for a leaf co-owned
	// by pi that the sync preserves. Loaded from req.Targets (the manifest set)
	// like the sync path.
	var siblingKnown map[string]bool
	if len(out.sharedPrefixes) > 0 {
		siblingEntries, serr := loadSiblingLedgerEntries(req.Root, req.Targets, target)
		if serr != nil {
			change.Error = serr.Error()
			return change
		}
		siblingKnown = make(map[string]bool, len(siblingEntries))
		for _, e := range siblingEntries {
			siblingKnown[e.Path] = true
		}
	}
	underSharedLeaf := func(p string) bool {
		return len(out.sharedPrefixes) > 0 && leafUnder(out.sharedPrefixes, p) != ""
	}

	desired := map[string]string{}     // path -> sha256 (write_file only)
	toolOwnedSeen := map[string]bool{} // tool-owned paths already classified
	for _, op := range out.ops {
		switch o := op.(type) {
		case contract.OpWriteFile:
			desired[o.Path] = sha256Hex(o.Content)
		case contract.OpWriteToolOwned:
			// Tool-owned files: drift = "would a sync rewrite this file?".
			// DryMerge runs the same merge the sync path uses and compares the
			// result to the on-disk bytes. Because the merge is idempotent, a
			// clean sync reports no drift here; an out-of-band edit inside the
			// managed slice reports drift; user content outside the slice never
			// does. (The old code unconditionally reported WouldUpdate for any
			// ledgered path, so validate always saw false drift.)
			entry := mergeEntryFrom(o)
			exists, changed, _, derr := merge.DryMerge(req.Root, o.Path, entry)
			if derr != nil {
				change.Error = derr.Error()
				return change
			}
			// Several entries can target one file (e.g. two mcp-server-entry
			// ops -> .mcp.json). Classify each path once: a missing file is a
			// create; an existing file an update if any of its entries changed.
			if toolOwnedSeen[o.Path] {
				continue
			}
			switch {
			case !exists:
				change.WouldCreate = append(change.WouldCreate, o.Path)
				toolOwnedSeen[o.Path] = true
			case changed:
				change.WouldUpdate = append(change.WouldUpdate, o.Path)
				toolOwnedSeen[o.Path] = true
			}
		}
	}

	nativeOps, nativeWarnings := harness.NativeOperationsForTarget(req.Fragments, target)
	for _, w := range nativeWarnings {
		change.Warnings = append(change.Warnings, w.Message)
	}
	nativeSeen := map[string]bool{}
	for _, op := range nativeOps {
		exists, changed, derr := merge.DryNativeMerge(req.Root, op.Path, op.Entries)
		if derr != nil {
			change.Error = derr.Error()
			return change
		}
		if nativeSeen[op.Path] {
			continue
		}
		switch {
		case !exists:
			change.WouldCreate = append(change.WouldCreate, op.Path)
		case changed:
			change.WouldUpdate = append(change.WouldUpdate, op.Path)
		}
		nativeSeen[op.Path] = true
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
			onDisk, rerr := readHash(req.Root, p)
			switch {
			case rerr == nil:
				if onDisk != prev.SHA256 {
					change.OutOfBand = append(change.OutOfBand, p)
				}
			case errors.Is(rerr, fs.ErrNotExist):
				// File expected by the ledger is gone — not an out-of-band
				// edit; the orphan/delete accounting covers absence.
			default:
				// A real read failure (permission, I/O) must not be silently
				// swallowed as "unchanged".
				change.Error = rerr.Error()
				return change
			}
		}
	}

	// Deletions: ledger paths under an owned subdir no longer desired.
	for _, e := range old.Entries {
		if ownerOf(effective, e.Path) == "" {
			continue
		}
		if _, stillWanted := desired[e.Path]; stillWanted {
			continue
		}
		// A sibling target still owns this shared-leaf path, so sync won't
		// delete it — don't report a phantom deletion (mirrors applyTarget).
		if underSharedLeaf(e.Path) && siblingKnown[e.Path] {
			continue
		}
		change.WouldDelete = append(change.WouldDelete, e.Path)
	}

	change.WouldCreate = sortedStrings(change.WouldCreate)
	change.WouldUpdate = sortedStrings(change.WouldUpdate)
	change.WouldDelete = sortedStrings(change.WouldDelete)
	change.OutOfBand = sortedStrings(change.OutOfBand)
	sort.Strings(change.Warnings)
	return change
}

// mergeEntryFrom builds a merge.MergeEntry from a tool-owned op. The
// adapterkit.ToolOwnedKind(o.Kind) cast bridges contract.ToolOwnedKind and
// adapterkit.ToolOwnedKind — two parallel string types kept separate by the
// contract/adapterkit package split, with identical underlying values. Shared
// by the sync (target.go) and validate (plan.go) paths so the entry is built
// identically on both.
func mergeEntryFrom(o contract.OpWriteToolOwned) merge.MergeEntry {
	return merge.MergeEntry{Kind: adapterkit.ToolOwnedKind(o.Kind), Locator: o.Locator, Content: o.Content}
}

// sortedStrings returns a sorted copy of in.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// readHash reads the file at relPath through the root and returns its
// sha256 hex. The error is returned verbatim (callers branch on
// fs.ErrNotExist) so real I/O/permission failures are never silently
// swallowed as "unchanged".
func readHash(root *fsroot.Root, relPath string) (string, error) {
	f, err := root.Inner().Open(relPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}
