package manifest

import "time"

// Manifest is the v1 schema for `.aienv.yaml`.
//
// NOTE: This is a strict schema. Unknown keys are rejected at load time,
// except for forward-compat extension keys that start with `x-`.
type Manifest struct {
	Version int `yaml:"version"`

	Canonical CanonicalSource `yaml:"canonical"`

	// Floating controls whether `canonical.ref` may remain floating at sync
	// time. Pinning is the default; floating must be explicitly opted into.
	Floating bool `yaml:"floating,omitempty"`

	// Targets is an optional list of tool targets to compile to. Empty means
	// "no targets" (a valid state for a workspace that is not yet configured).
	Targets []string `yaml:"targets,omitempty"`

	// Scope declares where the rendered config is intended to apply for the
	// target tools. v1 accepts: user, project, global.
	Scope string `yaml:"scope,omitempty"`

	Cache CacheConfig `yaml:"cache,omitempty"`

	Adapters []AdapterDecl `yaml:"adapters,omitempty"`

	// TrustedSHA is the project-level trust anchor. It mirrors `canonical.commit`
	// and is committed to git so CI can fail closed on drift. See plan decision #9.
	TrustedSHA string `yaml:"trusted_sha,omitempty"`

	Trust TrustConfig `yaml:"trust,omitempty"`
}

type CanonicalSource struct {
	// Exactly one of URL or LocalPath must be set.
	URL       string `yaml:"url,omitempty"`
	LocalPath string `yaml:"local_path,omitempty"`

	// Ref is an optional git ref name (branch, tag) used at init time before
	// resolving to Commit.
	Ref string `yaml:"ref,omitempty"`

	// Commit is the pinned git commit SHA (40 hex). Pinning is the default.
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

	// Pin requests that adapter sources be pinned. Reserved for later units.
	Pin bool `yaml:"pin,omitempty"`

	// TrustedSHA optionally pins the adapter source itself. Reserved for later units.
	TrustedSHA string `yaml:"trusted_sha,omitempty"`
}

type TrustConfig struct {
	// RequireAttestation is reserved for v2+.
	RequireAttestation bool `yaml:"require_attestation,omitempty"`

	// AllowNewSHAsUntil optionally allows a cooldown window where new SHAs
	// can be accepted without manual promotion. Reserved; the v1 trust model
	// uses explicit trust commands.
	AllowNewSHAsUntil *time.Time `yaml:"allow_new_shas_until,omitempty"`
}
