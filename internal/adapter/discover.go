package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/agent-sync/agent-sync/internal/manifest"
)

// adapterBinaryPrefix is the magic filename prefix that PATH discovery
// looks for. A binary named aienvs-adapter-<name> on PATH (with an
// optional .exe suffix on Windows) registers as an adapter named
// <name>. Mirrors the kubectl plugin convention.
const adapterBinaryPrefix = "aienvs-adapter-"

// Source describes how an adapter ended up in the registry. Determines
// precedence: workspace manifest > PATH > bundled.
type Source uint8

const (
	// SourceUnknown is the zero value; a registered adapter never has it.
	SourceUnknown Source = iota
	SourceWorkspaceManifest
	SourcePATH
	SourceBundled
)

// String renders Source as a human-readable token. Stable; used in
// error messages and logs.
func (s Source) String() string {
	switch s {
	case SourceWorkspaceManifest:
		return "workspace-manifest"
	case SourcePATH:
		return "path"
	case SourceBundled:
		return "bundled"
	default:
		return "unknown"
	}
}

// BundledAdapter is an adapter compiled into the agent-sync binary itself.
// The runtime spawns it as a goroutine speaking the wire protocol over
// io.Pipe. Run is invoked by the in-process transport in inproc.go.
type BundledAdapter struct {
	Manifest AdapterManifest

	// Run is the entry point invoked by the in-process transport.
	// stdin / stdout are pipes connected to the runtime — the bundled
	// adapter wraps them with contract.NewFrameReader and
	// contract.WriteFrame just as a subprocess adapter would. Returning
	// from Run signals the session is done; an error is surfaced to
	// the runtime as the adapter's exit reason.
	Run func(ctx context.Context, stdin io.Reader, stdout io.Writer) error
}

// Adapter is one entry in the discovered registry. Source records how
// the adapter was found; Manifest is the validated metadata used to
// spawn it; Bundled is non-nil when the adapter is compiled in.
type Adapter struct {
	Manifest AdapterManifest
	Source   Source
	Bundled  *BundledAdapter
}

// Registry holds the resolved adapter set. Construction is deterministic
// — Names returns adapters in sorted order so test output is stable
// across runs.
type Registry struct {
	byName map[string]*Adapter
}

// Names returns the registered adapter names in lexical order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Get returns the registered adapter for name, or false if no adapter
// is registered under that name.
func (r *Registry) Get(name string) (*Adapter, bool) {
	a, ok := r.byName[name]
	return a, ok
}

// DiscoverOptions controls discovery. All fields are optional; an empty
// DiscoverOptions yields an empty registry.
type DiscoverOptions struct {
	// Workspace is the loaded workspace manifest. Adapters declared in
	// its Adapters block are registered with SourceWorkspaceManifest
	// and take precedence over PATH and Bundled.
	Workspace *manifest.Manifest

	// PATH is the list of directories to search for aienvs-adapter-<name>
	// binaries. When nil, no PATH lookup is performed. Callers that want
	// the OS PATH should pass strings.Split(os.Getenv("PATH"), ...).
	PATH []string

	// Bundled is the list of compiled-in adapters to register.
	Bundled []*BundledAdapter
}

// Discovery-level sentinel errors. Callers branch with errors.Is.
var (
	// ErrAdapterPrefixNested is returned when two registered adapters
	// declare reserved_prefix values where one is a path-prefix of the
	// other. The R10 invariant from the parent plan: no nested adapter
	// ownership.
	ErrAdapterPrefixNested = errors.New("adapter: nested reserved_prefix is not allowed")
)

// DiscoverAdapters resolves the adapter registry from the configured
// sources. Precedence: workspace manifest > PATH > bundled. The same
// adapter name appearing in multiple sources is registered once,
// keyed by the highest-precedence source.
//
// Discovery has no side effects on the file system. ctx is honored for
// directory reads.
func DiscoverAdapters(ctx context.Context, opts DiscoverOptions) (*Registry, error) {
	r := &Registry{byName: map[string]*Adapter{}}

	if opts.Workspace != nil {
		for i := range opts.Workspace.Adapters {
			d := &opts.Workspace.Adapters[i]
			am, err := manifestFromDecl(d)
			if err != nil {
				return nil, fmt.Errorf("adapter: workspace manifest adapter %q: %w", d.Name, err)
			}
			r.byName[am.Name] = &Adapter{
				Manifest: *am,
				Source:   SourceWorkspaceManifest,
			}
		}
	}

	for _, dir := range opts.PATH {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Missing/inaccessible PATH dirs are normal; skip silently.
			if os.IsNotExist(err) {
				continue
			}
			// Permission errors and the like are also non-fatal — the
			// dir simply contributes nothing. Log via the runtime
			// (caller-side) once that's wired.
			continue
		}
		for _, e := range entries {
			name, ok := adapterNameFromBinary(e.Name())
			if !ok {
				continue
			}
			if _, already := r.byName[name]; already {
				// Lower-precedence source already lost — keep first.
				continue
			}
			// Pin the manifest's command to the absolute path discovered
			// in this directory. Storing just "aienvs-adapter-<name>"
			// would force a second $PATH resolution at spawn time, which
			// can race against the discovery scan (a different directory
			// earlier on PATH might shadow the binary we just validated).
			// Using the resolved path makes spawn TOCTOU-safe relative to
			// discovery.
			absPath, err := filepath.Abs(filepath.Join(dir, e.Name()))
			if err != nil {
				// Abs effectively never fails for a path we just read
				// out of os.ReadDir on a real OS, but if it does the
				// safest move is to skip this entry rather than register
				// an adapter we cannot reliably exec.
				continue
			}
			r.byName[name] = &Adapter{
				Manifest: *SyntheticAdapterManifest(name, absPath),
				Source:   SourcePATH,
			}
		}
	}

	for _, b := range opts.Bundled {
		if b == nil {
			continue
		}
		if _, already := r.byName[b.Manifest.Name]; already {
			continue
		}
		// Validate the bundled manifest the same way LoadAdapterManifestBytes
		// would; bundled adapters that ship invalid manifests should fail
		// fast in CI, not silently mis-register.
		if err := validateAdapterManifest(&b.Manifest); err != nil {
			return nil, fmt.Errorf("adapter: bundled adapter %q: %w", b.Manifest.Name, err)
		}
		copyManifest := b.Manifest
		r.byName[b.Manifest.Name] = &Adapter{
			Manifest: copyManifest,
			Source:   SourceBundled,
			Bundled:  b,
		}
	}

	if err := validateNoNestedPrefixes(r); err != nil {
		return nil, err
	}

	return r, nil
}

// manifestFromDecl converts a workspace manifest's AdapterDecl into a
// validated AdapterManifest. The workspace decl is intentionally narrow
// — it omits contract_version because the runtime always speaks
// ContractVersionV1; the adapter binary's adapter.yaml will be loaded
// at spawn time and will be the source of truth for contract_version.
func manifestFromDecl(d *manifest.AdapterDecl) (*AdapterManifest, error) {
	command := d.Command
	if len(command) == 0 {
		// Workspace manifest is allowed to omit command and rely on the
		// PATH-binary convention. Synthesize the default.
		command = []string{adapterBinaryPrefix + d.Name}
	}
	m := &AdapterManifest{
		Name:            d.Name,
		Version:         d.Version,
		ContractVersion: ContractVersionV1,
		Command:         command,
		// validateAdapterManifest runs ValidateReservedPrefix which
		// normalizes (path.Clean + trailing-slash trim) and rejects
		// absolute / Windows-volume / backslash / ".." shapes.
		ReservedPrefix: d.ReservedPrefix,
	}
	if err := validateAdapterManifest(m); err != nil {
		return nil, err
	}
	return m, nil
}

// adapterNameFromBinary returns the adapter name encoded in a PATH
// binary filename, or false if the filename doesn't match the
// aienvs-adapter-<name> shape. Empty-suffix binaries
// (aienvs-adapter- alone) are rejected. Windows .exe suffix is stripped.
func adapterNameFromBinary(filename string) (string, bool) {
	name := filename
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	if !strings.HasPrefix(name, adapterBinaryPrefix) {
		return "", false
	}
	suffix := strings.TrimPrefix(name, adapterBinaryPrefix)
	if suffix == "" {
		return "", false
	}
	if !adapterNamePattern.MatchString(suffix) {
		return "", false
	}
	return suffix, true
}

// validateNoNestedPrefixes asserts the R10 invariant from the parent
// plan: no two adapters declare reserved_prefix values where one is a
// path-prefix of the other. Adapters with empty reserved_prefix are
// exempt (they don't claim ownership).
func validateNoNestedPrefixes(r *Registry) error {
	type entry struct {
		name   string
		prefix string
	}
	var entries []entry
	for _, a := range r.byName {
		if a.Manifest.ReservedPrefix == "" {
			continue
		}
		entries = append(entries, entry{name: a.Manifest.Name, prefix: a.Manifest.ReservedPrefix})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].prefix < entries[j].prefix
	})
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if isPathPrefix(entries[i].prefix, entries[j].prefix) {
				return fmt.Errorf("%w: %q (adapter %q) is a prefix of %q (adapter %q)",
					ErrAdapterPrefixNested,
					entries[i].prefix, entries[i].name,
					entries[j].prefix, entries[j].name)
			}
		}
	}
	return nil
}

// isPathPrefix reports whether short is a path-segment prefix of long.
// Equal strings count as prefixes of each other (caller has already
// dedup'd by sorting). Both strings are forward-slash form (the YAML
// stores them this way per the schema's normalization).
//
// The workspace-root case "." is special: path.Clean turns "" or "./" into
// ".", and "." is a path-segment prefix of every relative path even though
// no real path literally starts with "./". An adapter declaring "." as
// reserved_prefix would otherwise nest every other adapter beneath itself
// without being detected by the HasPrefix check.
func isPathPrefix(short, long string) bool {
	if short == long {
		return true
	}
	if short == "." {
		return true
	}
	// Use forward slash as the path separator; reserved_prefix is a
	// workspace-relative path that always uses '/' on the wire and in
	// the manifest, regardless of OS. Avoid filepath.Separator here.
	return strings.HasPrefix(long, short+"/")
}
