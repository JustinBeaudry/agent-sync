package sync

import (
	"encoding/json"
	"fmt"

	"github.com/agent-sync/agent-sync/internal/fsroot"
)

// Status is the swap sentinel's lifecycle state.
type Status string

const (
	StatusIntend    Status = "intend"     // sentinel written, no rename yet
	StatusStep1Done Status = "step1_done" // prefix moved aside to prefix.old
	StatusStep2Done Status = "step2_done" // staging promoted to prefix
)

// Sentinel is the persisted `.state` record co-located in the staging
// generation dir. It is self-describing — it carries the rel paths the
// recovery reconciler needs so recovery never has to reconstruct them.
type Sentinel struct {
	Status         Status `json:"status"`
	Workspace      string `json:"workspace"`
	Target         string `json:"target"`
	SHA            string `json:"sha"`
	StartedAt      string `json:"started_at"`
	PrefixRel      string `json:"prefix_rel"`       // e.g. .claude/rules/agent-sync
	StagingLeafRel string `json:"staging_leaf_rel"` // e.g. .claude/rules/.agent-sync-staging/<gen>/agent-sync
}

func validStatus(s Status) bool {
	return s == StatusIntend || s == StatusStep1Done || s == StatusStep2Done
}

// writeSentinel atomically persists s at rel (workspace-relative). The
// staging dir is created by Stage, so the parent already exists.
func writeSentinel(root *fsroot.Root, rel string, s Sentinel) error {
	if !validStatus(s.Status) {
		return fmt.Errorf("sync: refusing to write sentinel with invalid status %q", s.Status)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("sync: marshal sentinel: %w", err)
	}
	data = append(data, '\n')
	if err := root.StagedWrite(rel, data, 0o600); err != nil {
		return fmt.Errorf("sync: write sentinel %s: %w", rel, err)
	}
	return nil
}

// readSentinel strict-decodes the sentinel at rel. An unknown status or
// unknown field is an error (we never act on a sentinel we don't fully
// understand).
func readSentinel(root *fsroot.Root, rel string) (Sentinel, error) {
	f, err := root.Inner().Open(rel)
	if err != nil {
		return Sentinel{}, err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var s Sentinel
	if err := dec.Decode(&s); err != nil {
		return Sentinel{}, fmt.Errorf("sync: decode sentinel %s: %w", rel, err)
	}
	if dec.More() {
		return Sentinel{}, fmt.Errorf("sync: trailing data in sentinel %s", rel)
	}
	if !validStatus(s.Status) {
		return Sentinel{}, fmt.Errorf("sync: sentinel %s has unknown status %q", rel, s.Status)
	}
	return s, nil
}
