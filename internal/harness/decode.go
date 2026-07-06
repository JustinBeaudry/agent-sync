package harness

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/agent-sync/agent-sync/internal/ir"
)

type fragmentYAML struct {
	ID          string      `yaml:"id"`
	Target      string      `yaml:"target"`
	Path        string      `yaml:"path"`
	Merge       MergeKind   `yaml:"merge"`
	Locator     string      `yaml:"locator"`
	Visibility  Visibility  `yaml:"visibility"`
	Inheritance Inheritance `yaml:"inheritance"`
	Safety      Safety      `yaml:"safety"`
	Payload     string      `yaml:"payload"`
}

// Decode walks source tree manifests under configs/**/fragment.yaml and
// materializes managed native fragments.
func Decode(src ir.SourceTree, ref string, opts DecodeOptions) ([]Fragment, []Warning, error) {
	entries, err := src.ReadTree(ref)
	if err != nil {
		return nil, nil, fmt.Errorf("harness: read tree: %w", err)
	}
	var manifestPaths []string
	for _, e := range entries {
		if path.Base(e.Path) == "fragment.yaml" && strings.HasPrefix(e.Path, "configs/") {
			manifestPaths = append(manifestPaths, e.Path)
		}
	}
	slices.Sort(manifestPaths)

	var out []Fragment
	for _, manifestPath := range manifestPaths {
		data, err := src.BlobContent(ref, manifestPath)
		if err != nil {
			return nil, nil, fmt.Errorf("harness: read %s: %w", manifestPath, err)
		}
		var fy fragmentYAML
		if err := yaml.UnmarshalWithOptions(data, &fy, yaml.DisallowUnknownField()); err != nil {
			return nil, nil, fmt.Errorf("harness: parse %s: %s", manifestPath, yaml.FormatError(err, false, true))
		}
		frag, err := buildFragment(src, ref, opts.Scope, manifestPath, fy)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, frag)
	}
	return out, nil, nil
}

func buildFragment(src ir.SourceTree, ref, scope, manifestPath string, fy fragmentYAML) (Fragment, error) {
	if !ir.IsValidID(fy.ID) {
		return Fragment{}, fmt.Errorf("harness: %s has invalid id %q", manifestPath, fy.ID)
	}
	if strings.TrimSpace(fy.Target) == "" || strings.ContainsAny(fy.Target, `/\`) {
		return Fragment{}, fmt.Errorf("harness: %s has invalid target %q", manifestPath, fy.Target)
	}
	if strings.TrimSpace(fy.Path) == "" {
		return Fragment{}, fmt.Errorf("harness: %s has empty path", manifestPath)
	}
	cleanPath, err := cleanWorkspacePath(fy.Path)
	if err != nil {
		return Fragment{}, fmt.Errorf("harness: %s path: %w", manifestPath, err)
	}

	switch fy.Merge {
	case MergeTOMLKey, MergeCodexHooks:
		// accepted
	default:
		return Fragment{}, fmt.Errorf("harness: %s merge: %q is not supported", manifestPath, fy.Merge)
	}
	if fy.Merge == MergeTOMLKey || fy.Merge == MergeCodexHooks {
		if strings.TrimSpace(fy.Locator) == "" {
			return Fragment{}, fmt.Errorf("harness: %s requires locator for merge kind %q", manifestPath, fy.Merge)
		}
	}
	if strings.TrimSpace(fy.Payload) == "" {
		return Fragment{}, fmt.Errorf("harness: %s requires payload", manifestPath)
	}

	payloadPath, err := cleanPayloadPath(path.Dir(manifestPath), fy.Payload)
	if err != nil {
		return Fragment{}, fmt.Errorf("harness: %s payload: %w", manifestPath, err)
	}
	payload, err := src.BlobContent(ref, payloadPath)
	if err != nil {
		return Fragment{}, fmt.Errorf("harness: read payload %s: %w", payloadPath, err)
	}

	visibility, inheritance := defaultsFor(scope)
	if fy.Visibility != "" {
		if !isValidVisibility(fy.Visibility) {
			return Fragment{}, fmt.Errorf("harness: %s has invalid visibility %q", manifestPath, fy.Visibility)
		}
		visibility = fy.Visibility
	}
	if fy.Inheritance != "" {
		if !isValidInheritance(fy.Inheritance) {
			return Fragment{}, fmt.Errorf("harness: %s has invalid inheritance %q", manifestPath, fy.Inheritance)
		}
		inheritance = fy.Inheritance
	}
	safety := fy.Safety
	if safety == "" {
		safety = SafetyPassive
	}
	if !isValidSafety(safety) {
		return Fragment{}, fmt.Errorf("harness: %s has invalid safety %q", manifestPath, safety)
	}
	if visibility == VisibilityMachineLocal {
		inheritance = InheritanceRootOnly
	}

	return Fragment{
		ID:          fy.ID,
		Target:      fy.Target,
		Path:        cleanPath,
		Merge:       fy.Merge,
		Locator:     fy.Locator,
		Visibility:  visibility,
		Inheritance: inheritance,
		Safety:      safety,
		PayloadPath: payloadPath,
		Payload:     payload,
		Scope:       scope,
		Provenance: ir.Provenance{
			Path: manifestPath,
		},
	}, nil
}

func defaultsFor(scope string) (Visibility, Inheritance) {
	switch scope {
	case "user":
		return VisibilityPersonal, InheritanceRootOnly
	case "workspace":
		return VisibilityTeam, InheritanceDescendants
	default:
		return VisibilityTeam, InheritanceRootOnly
	}
}

func isValidVisibility(v Visibility) bool {
	switch v {
	case VisibilityPersonal, VisibilityTeam, VisibilityMachineLocal:
		return true
	default:
		return false
	}
}

func isValidInheritance(i Inheritance) bool {
	switch i {
	case InheritanceRootOnly, InheritanceDescendants:
		return true
	default:
		return false
	}
}

func isValidSafety(s Safety) bool {
	switch s {
	case SafetyPassive, SafetyToolAccess, SafetyExecutable:
		return true
	default:
		return false
	}
}

func cleanWorkspacePath(p string) (string, error) {
	if p == "" || path.IsAbs(p) || strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("must be a non-empty relative forward-slash path")
	}
	clean := path.Clean(p)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("must stay inside the scope root")
	}
	return clean, nil
}

func cleanPayloadPath(base, rel string) (string, error) {
	if rel == "" || path.IsAbs(rel) || strings.ContainsRune(rel, '\\') {
		return "", fmt.Errorf("must be a relative forward-slash path")
	}
	joined := path.Clean(path.Join(base, rel))
	if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
		return "", fmt.Errorf("must not contain traversal")
	}
	for _, seg := range strings.Split(path.Clean(rel), "/") {
		if seg == ".." {
			return "", fmt.Errorf("must not contain ..")
		}
	}
	return joined, nil
}
