package manifest

const (
	ScopeUser      = "user"
	ScopeProject   = "project"
	ScopeWorkspace = "workspace"
	ScopeGlobal    = "global"
)

// Manifest is the v1 schema for `.agent-sync.yaml`.
//
// NOTE: This is a strict schema. Unknown keys are rejected at load time,
// except for forward-compat extension keys that start with `x-`.
type Manifest struct {
	Version int `yaml:"version"`

	Canonical CanonicalSource `yaml:"canonical"`

	// Targets is an optional list of tool targets to compile to. Empty means
	// "no targets" (a valid state for a workspace that is not yet configured).
	Targets []string `yaml:"targets,omitempty"`

	// Scope declares where the rendered config is intended to apply for the
	// target tools. v1 accepts: user, project, workspace, global.
	Scope string `yaml:"scope,omitempty"`

	// ActivationRoot marks this manifest as the workspace activation root.
	ActivationRoot bool `yaml:"activation_root,omitempty"`

	Cache CacheConfig `yaml:"cache,omitempty"`

	Adapters []AdapterDecl `yaml:"adapters,omitempty"`

	// TrustedSHA is the project-level trust anchor. It mirrors `canonical.commit`
	// and is committed to git so CI can fail closed on drift. See plan decision #9.
	TrustedSHA string `yaml:"trusted_sha,omitempty"`

	Trust TrustConfig `yaml:"trust,omitempty"`

	// Compose opts a project scope into hierarchy composition — folding a
	// broader scope's layer into this scope's output. Absent ⇒ zero value ⇒
	// no composition (current behavior). See plan
	// docs/plans/2026-07-01-002-feat-hierarchy-composition-plan.md (D2).
	Compose ComposeConfig `yaml:"compose,omitempty"`
}

// ComposeConfig is the opt-in block for hierarchy composition. Fields are
// namespaced by <adapter>-<what>-from-<source-scope> so future compose modes
// extend this block without a breaking rename.
type ComposeConfig struct {
	// CursorRulesFromUser, when true, folds the user-scope Cursor `rule` layer
	// into this project's .cursor/rules/ during a project sync. Cursor has no
	// file-addressable user-global rules location, so a user rule only takes
	// effect when written into each project's rules dir; this flag requests
	// that fold. Composed rules are owned by the project's ledger.
	CursorRulesFromUser bool `yaml:"cursor-rules-from-user,omitempty"`
}

type CanonicalSource struct {
	// Exactly one of URL, LocalPath, or LocalDir must be set.
	//
	//   - URL       — a remote git repository (cloned + pinned by Commit).
	//   - LocalPath — a local git repository / clone (opened + pinned by Commit).
	//   - LocalDir  — an in-repo working-tree directory read directly from the
	//                 filesystem. Unpinned by nature: it has no git object store,
	//                 no Commit, and is exempt from trust (TOFU) and offline-strict.
	URL       string `yaml:"url,omitempty"`
	LocalPath string `yaml:"local_path,omitempty"`

	// LocalDir is a workspace-relative directory (e.g. ".agents") whose contents
	// are compiled as the canonical source straight from the working tree. It is
	// mutually exclusive with URL/LocalPath and must not set Ref/Commit.
	LocalDir string `yaml:"local_dir,omitempty"`

	// Ref is an optional git ref name (branch, tag) used at init time before
	// resolving to Commit. Not valid for LocalDir.
	Ref string `yaml:"ref,omitempty"`

	// Commit is the pinned git commit SHA (40 hex). Pinning is the default for
	// the git-backed sources (URL/LocalPath). Not valid for LocalDir.
	Commit string `yaml:"commit,omitempty"`
}

type CacheConfig struct {
	// Override, if set, overrides the default cache root. This may be used for
	// workspace-local cache storage.
	Override string `yaml:"override,omitempty"`
}

type AdapterDecl struct {
	Name string `yaml:"name"`

	// Source may point to an out-of-tree adapter implementation. Bundled
	// adapters use an implicit source.
	Source string `yaml:"source,omitempty"`

	// Command, when set, overrides the argv slice the runtime would
	// otherwise infer from PATH. The first element is resolved against
	// $PATH unless it contains a path separator.
	Command []string `yaml:"command,omitempty"`

	// Version is a free-form version pin for the adapter. Compared by
	// humans; the runtime does not interpret it.
	Version string `yaml:"version,omitempty"`

	// ReservedPrefix is the path prefix (relative to the workspace root)
	// the adapter owns. Trailing slashes are stripped on load. The
	// runtime rejects configurations where one adapter's prefix is
	// nested inside another's.
	ReservedPrefix string `yaml:"reserved_prefix,omitempty"`
}

type TrustConfig struct {
}
