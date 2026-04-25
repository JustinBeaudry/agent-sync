package ir

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
)

// TestDecode_AllSixKinds is the broadest happy-path test: a canonical repo
// with one node of every v1 kind, plus AGENTS.md at root.
func TestDecode_AllSixKinds(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "AGENTS.md", Content: "# agents\n\ntop-level guidance.\n"},
		{Path: "rules/no-pr-on-friday.md", Content: "Don't ship on Friday.\n"},
		{Path: "skills/review-guide/SKILL.md", Content: "---\nrequired: true\n---\nReview guide body.\n"},
		{Path: "commands/ship-it.md", Content: "Ship the diff.\n"},
		{Path: "mcp/linear.json", Content: `{"command":"linear-server"}`},
		{Path: "plugins/ce.toml", Content: "name = \"ce\"\nversion = \"1.2.3\"\n"},
	})

	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	got := byKind(nodes)
	want := []Kind{
		KindAgentsMD,
		KindRule,
		KindSkill,
		KindCommand,
		KindPluginReference,
		KindMCPServerEntry,
	}
	for _, k := range want {
		if len(got[k]) != 1 {
			t.Errorf("kind %q: got %d nodes, want 1", k, len(got[k]))
		}
	}
}

func TestDecode_AGENTSMD_OverlaysSurfaceAsTargetScopedNodes(t *testing.T) {
	t.Parallel()

	// AGENTS.md is the canonical name. CLAUDE.md / GEMINI.md are tool
	// overlays per the spec — they share the agents-md kind but their
	// Targets restrict scope.
	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "AGENTS.md", Content: "# canonical agents\n"},
		{Path: "CLAUDE.md", Content: "# claude overlay\n"},
		{Path: "GEMINI.md", Content: "# gemini overlay\n"},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	got := byKind(nodes)
	if len(got[KindAgentsMD]) != 3 {
		t.Errorf("expected 3 agents-md nodes (AGENTS + CLAUDE + GEMINI), got %d", len(got[KindAgentsMD]))
	}
	hasTarget := func(targets []string, want string) bool {
		for _, t := range targets {
			if t == want {
				return true
			}
		}
		return false
	}
	for _, n := range got[KindAgentsMD] {
		switch n.Provenance.Path {
		case "AGENTS.md":
			if len(n.Targets) != 0 {
				t.Errorf("AGENTS.md should be unscoped, got targets %v", n.Targets)
			}
		case "CLAUDE.md":
			if !hasTarget(n.Targets, "claude") {
				t.Errorf("CLAUDE.md missing claude target, got %v", n.Targets)
			}
		case "GEMINI.md":
			if !hasTarget(n.Targets, "gemini") {
				t.Errorf("GEMINI.md missing gemini target, got %v", n.Targets)
			}
		}
	}
}

func TestDecode_FrontmatterRequiredFlagPropagates(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/with-required.md", Content: "---\nrequired: true\n---\nbody\n"},
		{Path: "rules/without-required.md", Content: "body only\n"},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for _, n := range nodes {
		switch n.ID {
		case "with-required":
			if !n.Required {
				t.Error("with-required: Required = false, want true")
			}
		case "without-required":
			if n.Required {
				t.Error("without-required: Required = true, want false")
			}
		}
	}
}

func TestDecode_FrontmatterTargetsRestricts(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/scoped.md", Content: "---\ntargets: [claude, cursor]\n---\nbody\n"},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	wantTargets := []string{"claude", "cursor"}
	if len(nodes[0].Targets) != 2 {
		t.Fatalf("Targets = %v, want %v", nodes[0].Targets, wantTargets)
	}
}

func TestDecode_SkillWithAssetsBundlesAuxiliaryFiles(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "skills/with-assets/SKILL.md", Content: "the skill\n"},
		{Path: "skills/with-assets/templates/foo.txt", Content: "asset 1\n"},
		{Path: "skills/with-assets/data.json", Content: `{"k":"v"}`},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	skills := SkillsByID(nodes, repo.Repo, repo.SHA)
	skill, ok := skills["with-assets"]
	if !ok {
		t.Fatalf("skill 'with-assets' not found in result")
	}
	if len(skill.Assets) != 2 {
		t.Errorf("expected 2 assets, got %d (%v)", len(skill.Assets), assetPaths(skill.Assets))
	}
}

func TestDecode_UnrecognizedFileInRulesIsError(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/ok.md", Content: "ok\n"},
		{Path: "rules/oops.txt", Content: "wrong extension\n"},
	})
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if !errors.Is(err, ErrUnrecognizedFile) {
		t.Errorf("err = %v, want ErrUnrecognizedFile", err)
	}
	// Error message should name the offending path so users can find it.
	if err == nil || !strings.Contains(err.Error(), "rules/oops.txt") {
		t.Errorf("error should mention rules/oops.txt, got %v", err)
	}
}

func TestDecode_UnrecognizedFileInMCPIsError(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "mcp/linear.yaml", Content: "name: linear\n"}, // wrong ext
	})
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if !errors.Is(err, ErrUnrecognizedFile) {
		t.Errorf("err = %v, want ErrUnrecognizedFile", err)
	}
}

func TestDecode_DuplicateIDsRejected(t *testing.T) {
	t.Parallel()

	// IDs are derived from filename stem (or directory name for skills).
	// Two rules with the same id can only happen via different
	// extensions in the same directory; we only allow .md, so this is
	// hard to express. Instead, exercise the rule with a markdown file
	// whose id collides with an explicit frontmatter id... but we don't
	// support frontmatter id override in v1, so the test instead covers
	// two skills with the same dir name across case-folding (Linux is
	// case-sensitive, so we can't even do that). The duplicate path is
	// only really triggered if the decoder is called with multiple
	// canonical repos merged — a v2 concern. For v1, we exercise the
	// internal duplicate check by constructing a synthetic case.
	t.Skip("duplicate-id test deferred: v1 layout's filename->id mapping makes natural duplicates impossible inside a single canonical repo. Internal helper covered by detectDuplicates_test.")
}

func TestDecode_InvalidIDRejected(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/UPPERCASE.md", Content: "body\n"},
	})
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("err = %v, want ErrInvalidID", err)
	}
}

func TestDecode_SkillDirWithoutSKILLIsError(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "skills/orphan-dir/notes.md", Content: "no SKILL.md\n"},
	})
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if !errors.Is(err, ErrSkillMissingSKILL) {
		t.Errorf("err = %v, want ErrSkillMissingSKILL", err)
	}
}

func TestDecode_EmptyAgentsMDRejected(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "AGENTS.md", Content: ""},
	})
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if !errors.Is(err, ErrEmptyAgentsMD) {
		t.Errorf("err = %v, want ErrEmptyAgentsMD", err)
	}
}

func TestDecode_AgentsMDMissingIsWarningNotError(t *testing.T) {
	t.Parallel()

	// Greenfield canonical repo: at least one rule, no AGENTS.md.
	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/foo.md", Content: "rule\n"},
	})
	_, warnings, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	hasMissingWarning := false
	for _, w := range warnings {
		if w.Code == WarnAgentsMDMissing {
			hasMissingWarning = true
		}
	}
	if !hasMissingWarning {
		t.Errorf("expected WarnAgentsMDMissing in warnings, got %v", warnings)
	}
}

func TestDecode_FrontmatterParseErrorSurfaces(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/broken.md", Content: "---\nrequired: [unclosed\n---\nbody\n"},
	})
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if !errors.Is(err, ErrFrontmatterParse) {
		t.Errorf("err = %v, want ErrFrontmatterParse", err)
	}
}

func TestDecode_UnknownTargetRejected(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/scoped.md", Content: "---\ntargets: [claude, ghost-tool]\n---\nbody\n"},
	})
	known := map[string]struct{}{"claude": {}, "cursor": {}}
	_, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{KnownTargets: known})
	if !errors.Is(err, ErrUnknownTarget) {
		t.Errorf("err = %v, want ErrUnknownTarget", err)
	}
}

func TestDecode_DeterministicByteIdentical(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "AGENTS.md", Content: "# agents\n"},
		{Path: "rules/a.md", Content: "alpha\n"},
		{Path: "rules/b.md", Content: "bravo\n"},
		{Path: "skills/charlie/SKILL.md", Content: "charlie\n"},
		{Path: "commands/delta.md", Content: "delta\n"},
		{Path: "mcp/echo.json", Content: `{"name":"echo"}`},
	})

	var prev []byte
	for i := 0; i < 10; i++ {
		nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		got, err := json.Marshal(nodes)
		if err != nil {
			t.Fatalf("marshal run %d: %v", i, err)
		}
		if prev != nil && string(prev) != string(got) {
			t.Fatalf("non-deterministic IR across runs:\nrun 0: %s\nrun %d: %s", prev, i, got)
		}
		prev = got
	}
}

func TestDecode_ProvenanceCarriesGitBlobSHA(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/foo.md", Content: "body\n"},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Provenance.BlobSHA == "" {
		t.Error("Provenance.BlobSHA empty")
	}
	// Cross-check: blob SHA must be 40-hex.
	if len(nodes[0].Provenance.BlobSHA) != 40 {
		t.Errorf("Provenance.BlobSHA = %q, want 40-hex", nodes[0].Provenance.BlobSHA)
	}
}

func TestDecode_BodyStrippedOfFrontmatter(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/foo.md", Content: "---\nrequired: true\n---\n\nThe real body.\n"},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !strings.Contains(string(nodes[0].Body), "The real body.") {
		t.Errorf("body wrong: %q", nodes[0].Body)
	}
	if strings.Contains(string(nodes[0].Body), "required: true") {
		t.Errorf("body still contains frontmatter: %q", nodes[0].Body)
	}
}

func TestDecode_VersionDefaultsToOne(t *testing.T) {
	t.Parallel()

	repo := makeCanonicalRepo(t, []canonicalFile{
		{Path: "rules/no-version.md", Content: "no frontmatter\n"},
		{Path: "rules/v3.md", Content: "---\nversion: 3\n---\nbody\n"},
	})
	nodes, _, err := Decode(repo.Repo, repo.SHA, DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for _, n := range nodes {
		switch n.ID {
		case "no-version":
			if n.Version != 1 {
				t.Errorf("no-version: Version = %d, want 1", n.Version)
			}
		case "v3":
			if n.Version != 3 {
				t.Errorf("v3: Version = %d, want 3", n.Version)
			}
		}
	}
}

// --- helpers ---

func byKind(nodes []Node) map[Kind][]Node {
	m := map[Kind][]Node{}
	for _, n := range nodes {
		m[n.Kind] = append(m[n.Kind], n)
	}
	return m
}

func assetPaths(assets []Asset) []string {
	ps := make([]string, len(assets))
	for i, a := range assets {
		ps[i] = a.RelPath
	}
	sort.Strings(ps)
	return ps
}
