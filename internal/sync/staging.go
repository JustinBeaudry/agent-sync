package sync

import (
	"fmt"
	"path"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// stagingDirName is the per-parent scratch directory that holds staged
// generations, a sibling of the live reserved prefix.
const stagingDirName = ".agent-sync-staging"

// Meta is the deterministic identity of one staged generation. The
// caller stamps Timestamp and SHA (the core takes no wall-clock), so
// tests are deterministic and resumes are reproducible. Timestamp must
// be a fixed-width, lexically-sortable instant (e.g. 20260608T194149Z)
// so generation dirs sort chronologically for retention pruning.
type Meta struct {
	Timestamp string
	SHA       string
}

func (m Meta) gen() string { return m.Timestamp + "-" + m.SHA }

// stagingGenRel returns <parentRel>/.agent-sync-staging/<gen>.
func stagingGenRel(parentRel string, m Meta) string {
	return path.Join(parentRel, stagingDirName, m.gen())
}

// Stage creates <parentRel>/.agent-sync-staging/<gen>/<leaf>/ and returns
// its workspace-relative path. The caller writes the new generation's
// contents into the returned dir, then calls Swap to promote it.
func Stage(root *fsroot.Root, parentRel, leaf string, m Meta) (stagingLeafRel string, err error) {
	if m.Timestamp == "" || m.SHA == "" {
		return "", fmt.Errorf("sync: stage requires a Timestamp and SHA")
	}
	rel := path.Join(stagingGenRel(parentRel, m), leaf)
	if err := root.Inner().MkdirAll(rel, 0o755); err != nil {
		return "", fmt.Errorf("sync: create staging %s: %w", rel, err)
	}
	return rel, nil
}
