// Package wizard holds the Bubble Tea init wizard and the InitConfig it
// produces. InitConfig is the single convergence point (KTD-4): both the
// interactive wizard and the CLI's non-interactive flag path populate the
// same struct, and one manifest writer consumes it — so flag parity is
// structural, not a matter of discipline.
package wizard

import (
	"errors"
	"fmt"
	"strings"
)

// InitConfig is every decision needed to write a fresh manifest. Both the
// wizard and the non-interactive flag path fill it in.
type InitConfig struct {
	// Dir is the workspace directory to initialize (where .agent-sync.yaml is
	// written). Empty means the current directory.
	Dir string

	// Exactly one of SourceURL / LocalPath / LocalDir is set.
	SourceURL string
	LocalPath string

	// LocalDir is an in-repo working-tree source directory (e.g. ".agents").
	// It is unpinned by nature: no Ref/Commit/Floating apply.
	LocalDir string

	// Ref is the optional git ref (branch/tag) resolved to a SHA at init
	// time unless Floating.
	Ref string

	// Commit is the resolved/pinned SHA. Empty + !Floating means the
	// caller still needs to resolve it (URL path) before writing.
	Commit string

	// Floating opts out of pinning (invariant #4: pinning is default).
	Floating bool

	// Targets are the selected adapter target names.
	Targets []string
}

// Validate checks the config is internally consistent before a manifest is
// written.
func (c InitConfig) Validate() error {
	hasURL := strings.TrimSpace(c.SourceURL) != ""
	hasLocal := strings.TrimSpace(c.LocalPath) != ""
	hasDir := strings.TrimSpace(c.LocalDir) != ""
	n := 0
	for _, set := range []bool{hasURL, hasLocal, hasDir} {
		if set {
			n++
		}
	}
	if n != 1 {
		return errors.New("wizard: set exactly one of source URL, local path, or local directory")
	}

	// An in-repo local_dir source is unpinned by nature: a ref/commit/floating
	// pin is a configuration error rather than a no-op. Reject the combination
	// here so init never writes a manifest that load would refuse.
	if hasDir {
		if c.Commit != "" || strings.TrimSpace(c.Ref) != "" || c.Floating {
			return errors.New("wizard: a local directory is an unpinned working-tree source; it cannot set a ref, commit, or --floating")
		}
		return nil
	}

	if !c.Floating && c.Commit == "" {
		return errors.New("wizard: a pinned commit is required unless --floating is set")
	}
	if c.Floating && c.Commit != "" {
		return errors.New("wizard: --floating and a pinned commit are mutually exclusive")
	}
	// A floating local_path is rejected up front because sync cannot resolve
	// a moving local HEAD (ErrFloatingLocalUnsupported). Failing here keeps
	// init from writing a manifest sync would immediately refuse — a local
	// path must be pinned to a commit.
	if hasLocal && c.Floating {
		return errors.New("wizard: a local path must be pinned to a commit; --floating is not supported for local_path")
	}
	return nil
}

// ManifestYAML renders the config as a .agent-sync.yaml document. The output
// round-trips through manifest.LoadFile. Validate must pass first.
func (c InitConfig) ManifestYAML() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("version: 1\n")
	b.WriteString("canonical:\n")
	switch {
	case c.SourceURL != "":
		fmt.Fprintf(&b, "  url: %s\n", c.SourceURL)
	case c.LocalDir != "":
		// An in-repo working-tree source: just the directory, no ref/commit.
		fmt.Fprintf(&b, "  local_dir: %s\n", c.LocalDir)
	default:
		fmt.Fprintf(&b, "  local_path: %s\n", c.LocalPath)
	}
	if c.Ref != "" {
		fmt.Fprintf(&b, "  ref: %s\n", c.Ref)
	}
	if c.Commit != "" {
		fmt.Fprintf(&b, "  commit: %s\n", c.Commit)
	}
	// A floating manifest is represented by the absence of commit/
	// trusted_sha; the schema has no explicit `floating` key.
	if c.Commit != "" {
		// trusted_sha mirrors the pin (the project trust anchor).
		fmt.Fprintf(&b, "trusted_sha: %s\n", c.Commit)
	}
	if len(c.Targets) > 0 {
		b.WriteString("targets:\n")
		for _, t := range c.Targets {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
	}
	return []byte(b.String()), nil
}
