package wizard

import (
	"context"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/aienvs/aienvs/internal/tui"
)

// schemeRE matches a URL scheme prefix (https://, ssh://, git://, file://).
// winDriveRE matches a Windows drive-letter path (C:\ or C:/).
var (
	schemeRE   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	winDriveRE = regexp.MustCompile(`^[a-zA-Z]:[\\/]`)
)

// Run drives the interactive init wizard and returns the collected
// InitConfig. availableTargets is the discovered adapter name set the user
// chooses from. The wizard renders to out (stderr/the TTY); committed is
// false if the user aborted.
//
// The wizard collects source, ref, and target selection; pin resolution
// (turning a ref into a commit for URL sources) is performed by the caller
// after the wizard returns, so this package stays free of git/network I/O.
func Run(ctx context.Context, in io.Reader, out io.Writer, noColor bool, availableTargets []string) (cfg InitConfig, committed bool, err error) {
	model := newInitModel(noColor, availableTargets)
	final, runErr := tui.Run(ctx, model, in, out)
	if runErr != nil {
		return InitConfig{}, false, runErr
	}
	m := final.(*initModel)
	return m.cfg, m.committed, nil
}

type phase int

const (
	phaseSource phase = iota
	phaseRef
	phaseTargets
	phaseConfirm
	phaseDone
)

// initModel is the single-model wizard (a small linear state machine; the
// nav stack is reserved for the deeper branching the master plan
// enumerates). Each phase reads input and advances.
type initModel struct {
	theme   tui.Theme
	phase   phase
	input   textinput.Model
	targets []targetChoice
	cursor  int

	cfg       InitConfig
	committed bool
	quitting  bool
}

type targetChoice struct {
	name     string
	selected bool
}

func newInitModel(noColor bool, available []string) *initModel {
	ti := textinput.New()
	ti.Placeholder = "https://github.com/org/repo.git  (or an absolute local path)"
	ti.Focus()
	ti.Width = 60

	choices := make([]targetChoice, 0, len(available))
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)
	for _, n := range sorted {
		choices = append(choices, targetChoice{name: n, selected: true})
	}

	return &initModel{
		theme:   tui.NewTheme(noColor),
		phase:   phaseSource,
		input:   ti,
		targets: choices,
	}
}

func (m *initModel) Init() tea.Cmd { return textinput.Blink }

func (m *initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if isKey && (key.Type == tea.KeyCtrlC || key.Type == tea.KeyEsc) {
		m.quitting = true
		m.committed = false
		return m, tea.Quit
	}

	switch m.phase {
	case phaseSource:
		return m.updateSource(msg, key, isKey)
	case phaseRef:
		return m.updateRef(msg, key, isKey)
	case phaseTargets:
		return m.updateTargets(key, isKey)
	case phaseConfirm:
		return m.updateConfirm(key, isKey)
	}
	return m, nil
}

func (m *initModel) updateSource(msg tea.Msg, key tea.KeyMsg, isKey bool) (tea.Model, tea.Cmd) {
	if isKey && key.Type == tea.KeyEnter {
		val := strings.TrimSpace(m.input.Value())
		if val == "" {
			return m, nil // require a source
		}
		// Heuristic: an absolute path or one with a separator and no scheme
		// is a local path; otherwise a URL.
		if looksLikeLocalPath(val) {
			m.cfg.LocalPath = val
		} else {
			m.cfg.SourceURL = val
		}
		m.phase = phaseRef
		m.input.SetValue("")
		m.input.Placeholder = "main  (branch or tag; leave blank to skip)"
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *initModel) updateRef(msg tea.Msg, key tea.KeyMsg, isKey bool) (tea.Model, tea.Cmd) {
	if isKey && key.Type == tea.KeyEnter {
		m.cfg.Ref = strings.TrimSpace(m.input.Value())
		m.phase = phaseTargets
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *initModel) updateTargets(key tea.KeyMsg, isKey bool) (tea.Model, tea.Cmd) {
	if !isKey {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.targets)-1 {
			m.cursor++
		}
	case " ":
		if len(m.targets) > 0 {
			m.targets[m.cursor].selected = !m.targets[m.cursor].selected
		}
	case "enter":
		for _, t := range m.targets {
			if t.selected {
				m.cfg.Targets = append(m.cfg.Targets, t.name)
			}
		}
		m.phase = phaseConfirm
	}
	return m, nil
}

func (m *initModel) updateConfirm(key tea.KeyMsg, isKey bool) (tea.Model, tea.Cmd) {
	if !isKey {
		return m, nil
	}
	switch key.String() {
	case "y", "Y", "enter":
		m.committed = true
		m.phase = phaseDone
		return m, tea.Quit
	case "n", "N":
		m.committed = false
		m.phase = phaseDone
		return m, tea.Quit
	}
	return m, nil
}

func (m *initModel) View() string {
	if m.quitting {
		return m.theme.Help.Render("aborted") + "\n"
	}
	var b strings.Builder
	b.WriteString(m.theme.Title.Render("aienvs init") + "\n\n")
	switch m.phase {
	case phaseSource:
		b.WriteString(m.theme.Prompt.Render("Canonical source (URL or local path):") + "\n")
		b.WriteString(m.input.View() + "\n")
	case phaseRef:
		b.WriteString(m.theme.Prompt.Render("Ref to track:") + "\n")
		b.WriteString(m.input.View() + "\n")
	case phaseTargets:
		b.WriteString(m.theme.Prompt.Render("Select targets (space to toggle, enter to confirm):") + "\n")
		for i, t := range m.targets {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			box := "[ ]"
			if t.selected {
				box = "[x]"
			}
			line := cursor + box + " " + t.name
			if t.selected {
				line = m.theme.Selected.Render(line)
			}
			b.WriteString(line + "\n")
		}
	case phaseConfirm:
		b.WriteString(m.theme.Prompt.Render("Write manifest with:") + "\n")
		b.WriteString("  source:  " + m.sourceDisplay() + "\n")
		if m.cfg.Ref != "" {
			b.WriteString("  ref:     " + m.cfg.Ref + "\n")
		}
		b.WriteString("  targets: " + strings.Join(m.cfg.Targets, ", ") + "\n\n")
		b.WriteString(m.theme.Prompt.Render("Proceed? [Y/n]") + "\n")
	}
	b.WriteString("\n" + m.theme.Help.Render("ctrl+c/esc to abort") + "\n")
	return b.String()
}

func (m *initModel) sourceDisplay() string {
	if m.cfg.SourceURL != "" {
		return m.cfg.SourceURL
	}
	return m.cfg.LocalPath
}

// looksLikeLocalPath reports whether s is a filesystem path rather than a
// git URL. A string is a URL only if it carries a scheme (foo://) or is
// scp-style (user@host:path); everything else — POSIX absolute/relative
// paths, bare names, and Windows drive paths (C:\repo) — is treated as a
// local path. The default is "local", because a git remote always has a
// recognizable scheme or scp shape, whereas local paths take many forms.
func looksLikeLocalPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Scheme-bearing URL: https://, ssh://, git://, file://, etc.
	if schemeRE.MatchString(s) {
		return false
	}
	// scp-style remote: user@host:path (an '@' before the first ':').
	if at := strings.IndexByte(s, '@'); at >= 0 {
		if colon := strings.IndexByte(s, ':'); colon > at {
			return false
		}
	}
	// Windows drive path (C:\ or C:/) is local, even though it contains ':'.
	if winDriveRE.MatchString(s) {
		return true
	}
	// A bare "host:path" with no '@' and no drive letter is ambiguous; treat
	// a single-letter prefix before ':' as a Windows drive (handled above)
	// and anything else with a ':' as an scp-style host:path remote.
	if strings.Contains(s, ":") {
		return false
	}
	// No scheme, no scp shape, no ambiguous colon → a local path
	// (absolute, ./, ../, ~, or a bare relative name like "repo/sub").
	return true
}
