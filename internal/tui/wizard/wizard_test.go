package wizard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInitConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     InitConfig
		wantErr bool
	}{
		{"url pinned ok", InitConfig{SourceURL: "https://x/y.git", Commit: "abc"}, false},
		{"local pinned ok", InitConfig{LocalPath: "/repo", Commit: "abc"}, false},
		{"floating url ok", InitConfig{SourceURL: "https://x/y.git", Floating: true}, false},
		{"both sources", InitConfig{SourceURL: "u", LocalPath: "/p", Commit: "abc"}, true},
		{"no source", InitConfig{Commit: "abc"}, true},
		{"unpinned non-floating", InitConfig{SourceURL: "u"}, true},
		{"floating with commit", InitConfig{SourceURL: "u", Floating: true, Commit: "abc"}, true},
		{"local_dir ok", InitConfig{LocalDir: ".agents"}, false},
		{"local_dir + commit", InitConfig{LocalDir: ".agents", Commit: "abc"}, true},
		{"local_dir + ref", InitConfig{LocalDir: ".agents", Ref: "main"}, true},
		{"local_dir + floating", InitConfig{LocalDir: ".agents", Floating: true}, true},
		{"local_dir + url", InitConfig{LocalDir: ".agents", SourceURL: "u"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestInitConfig_ManifestYAML(t *testing.T) {
	cfg := InitConfig{
		SourceURL: "https://github.com/org/repo.git",
		Ref:       "main",
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		Targets:   []string{"claude", "cursor"},
	}
	out, err := cfg.ManifestYAML()
	if err != nil {
		t.Fatalf("ManifestYAML: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"version: 1",
		"url: https://github.com/org/repo.git",
		"ref: main",
		"commit: 0123456789abcdef0123456789abcdef01234567",
		"trusted_sha: 0123456789abcdef0123456789abcdef01234567",
		"- claude",
		"- cursor",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
	// A floating manifest must NOT emit a commit/trusted_sha or a floating key.
	floatOut, ferr := InitConfig{SourceURL: "u", Floating: true}.ManifestYAML()
	if ferr != nil {
		t.Fatalf("floating ManifestYAML: %v", ferr)
	}
	if strings.Contains(string(floatOut), "commit:") || strings.Contains(string(floatOut), "trusted_sha:") {
		t.Errorf("floating manifest should not pin:\n%s", floatOut)
	}
	if strings.Contains(string(floatOut), "floating:") {
		t.Errorf("schema has no floating key; should not be emitted:\n%s", floatOut)
	}

	// A local_dir manifest emits the directory and nothing pin-related.
	dirOut, derr := InitConfig{LocalDir: ".agents", Targets: []string{"claude"}}.ManifestYAML()
	if derr != nil {
		t.Fatalf("local_dir ManifestYAML: %v", derr)
	}
	ds := string(dirOut)
	if !strings.Contains(ds, "local_dir: .agents") {
		t.Errorf("local_dir manifest missing the directory:\n%s", ds)
	}
	for _, unwanted := range []string{"url:", "local_path:", "ref:", "commit:", "trusted_sha:"} {
		if strings.Contains(ds, unwanted) {
			t.Errorf("local_dir manifest should not emit %q:\n%s", unwanted, ds)
		}
	}
}

func TestInitConfig_ManifestYAML_RendersScopeAndActivationRoot(t *testing.T) {
	out, err := InitConfig{
		LocalDir:       ".agents",
		Scope:          "workspace",
		ActivationRoot: true,
		Targets:        []string{"codex"},
	}.ManifestYAML()
	if err != nil {
		t.Fatalf("ManifestYAML: %v", err)
	}
	text := string(out)
	for _, want := range []string{"scope: workspace\n", "activation_root: true\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}

func TestLooksLikeLocalPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/org/repo.git", false},
		{"ssh://git@github.com/org/repo.git", false},
		{"git@github.com:org/repo.git", false}, // scp-style
		{"/abs/path/to/repo", true},
		{"./rel", true},
		{"../rel", true},
		{"~/repo", true},
		{"my-repo", true},         // bare relative name
		{"foo/bar", true},         // relative path, no scheme
		{`C:\path\to\repo`, true}, // windows drive
		{"C:/path/to/repo", true},
	}
	for _, c := range cases {
		if got := looksLikeLocalPath(c.in); got != c.want {
			t.Errorf("looksLikeLocalPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestInitConfig_RejectsFloatingLocalPath(t *testing.T) {
	err := InitConfig{LocalPath: "/repo", Floating: true}.Validate()
	if err == nil {
		t.Fatal("floating local_path must be rejected by Validate")
	}
}

// key builds a rune keypress message.
func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func typeString(m *initModel, s string) {
	for _, r := range s {
		m.Update(key(r))
	}
}

// TestInitModel_EmptyEnterDefaultsToAgentsLocalDir pins plan R10: an empty
// Enter on the source screen accepts the in-repo .agents default and skips
// the ref phase (ref + local_dir is invalid).
func TestInitModel_EmptyEnterDefaultsToAgentsLocalDir(t *testing.T) {
	m := newInitModel(true, []string{"claude"}, nil)

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cfg.LocalDir != ".agents" {
		t.Fatalf("LocalDir = %q, want .agents", m.cfg.LocalDir)
	}
	if m.phase != phaseTargets {
		t.Fatalf("phase = %v, want phaseTargets (ref must be skipped for local_dir)", m.phase)
	}

	// Finish the flow: accept targets, confirm.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(key('y'))
	if !m.committed {
		t.Fatal("expected committed=true")
	}
	if err := m.cfg.Validate(); err != nil {
		t.Fatalf("committed config must validate: %v", err)
	}
}

// TestInitModel_DiscoveredTargetsPreselected pins plan R9: when discovery
// found footprints, only those start selected.
func TestInitModel_DiscoveredTargetsPreselected(t *testing.T) {
	m := newInitModel(true, []string{"claude", "cursor", "pi"}, []string{"cursor"})

	for _, tc := range m.targets {
		want := tc.name == "cursor"
		if tc.selected != want {
			t.Fatalf("target %s selected = %v, want %v", tc.name, tc.selected, want)
		}
	}
}

// TestInitModel_ZeroDiscoveredPreselectsAll pins the greenfield fallback
// (plan R9): zero discovered footprints keeps today's select-all default.
func TestInitModel_ZeroDiscoveredPreselectsAll(t *testing.T) {
	m := newInitModel(true, []string{"claude", "cursor"}, nil)

	for _, tc := range m.targets {
		if !tc.selected {
			t.Fatalf("target %s should start selected when nothing was discovered", tc.name)
		}
	}
}

// TestInitModel_ConfirmRendersLocalDirAndEmptyTargets: the confirm screen
// must show the defaulted source and an explicit "(none)" for zero targets.
func TestInitModel_ConfirmRendersLocalDirAndEmptyTargets(t *testing.T) {
	m := newInitModel(true, []string{"claude"}, nil)

	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // empty source → .agents
	m.Update(key(' '))                       // deselect claude
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm targets (none)

	view := m.View()
	if !strings.Contains(view, ".agents") {
		t.Fatalf("confirm view should render the .agents source:\n%s", view)
	}
	if !strings.Contains(view, "(none)") {
		t.Fatalf("confirm view should render (none) for empty targets:\n%s", view)
	}
}

func TestInitModel_FullFlowProducesConfig(t *testing.T) {
	m := newInitModel(true, []string{"claude", "cursor"}, nil)

	// Phase source: type a URL, Enter.
	typeString(m, "https://github.com/org/repo.git")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseRef {
		t.Fatalf("after source, phase = %v, want phaseRef", m.phase)
	}

	// Phase ref: type "main", Enter.
	typeString(m, "main")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseTargets {
		t.Fatalf("after ref, phase = %v, want phaseTargets", m.phase)
	}

	// Phase targets: both start selected; toggle cursor item (claude) off,
	// then confirm with Enter → only cursor remains.
	m.Update(key(' ')) // toggle claude off
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseConfirm {
		t.Fatalf("after targets, phase = %v, want phaseConfirm", m.phase)
	}

	// Phase confirm: press y.
	m.Update(key('y'))
	if !m.committed {
		t.Fatal("expected committed=true after y")
	}
	if m.cfg.SourceURL != "https://github.com/org/repo.git" {
		t.Errorf("source = %q", m.cfg.SourceURL)
	}
	if m.cfg.Ref != "main" {
		t.Errorf("ref = %q", m.cfg.Ref)
	}
	// claude toggled off → only cursor selected.
	if len(m.cfg.Targets) != 1 || m.cfg.Targets[0] != "cursor" {
		t.Errorf("targets = %v, want [cursor]", m.cfg.Targets)
	}
}

func TestInitModel_EscAborts(t *testing.T) {
	m := newInitModel(true, []string{"claude"}, nil)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.committed {
		t.Fatal("esc should not commit")
	}
	if !m.quitting {
		t.Fatal("esc should set quitting")
	}
}
