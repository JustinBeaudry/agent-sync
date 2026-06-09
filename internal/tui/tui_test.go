package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInteractive(t *testing.T) {
	cases := []struct {
		isTTY, nonInteractive, accessible, want bool
	}{
		{true, false, false, true},
		{false, false, false, false}, // not a tty
		{true, true, false, false},   // forced non-interactive
		{true, false, true, false},   // accessible mode
		{false, true, true, false},
	}
	for _, c := range cases {
		if got := Interactive(c.isTTY, c.nonInteractive, c.accessible); got != c.want {
			t.Errorf("Interactive(tty=%v nonint=%v acc=%v) = %v, want %v",
				c.isTTY, c.nonInteractive, c.accessible, got, c.want)
		}
	}
}

// staticScreen is a minimal screen rendering a fixed label.
type staticScreen struct{ label string }

func (s staticScreen) Init() tea.Cmd                       { return nil }
func (s staticScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }
func (s staticScreen) View() string                        { return s.label }

func TestNav_PushPopDepth(t *testing.T) {
	nav := NewNav(staticScreen{label: "root"})
	if nav.Depth() != 1 {
		t.Fatalf("initial depth = %d, want 1", nav.Depth())
	}
	if nav.View() != "root" {
		t.Fatalf("view = %q, want root", nav.View())
	}

	// Push a second screen.
	nav.Update(PushMsg{Screen: staticScreen{label: "second"}})
	if nav.Depth() != 2 {
		t.Fatalf("after push depth = %d, want 2", nav.Depth())
	}
	if nav.View() != "second" {
		t.Fatalf("after push view = %q, want second", nav.View())
	}

	// Pop back to root.
	nav.Update(PopMsg{})
	if nav.Depth() != 1 {
		t.Fatalf("after pop depth = %d, want 1", nav.Depth())
	}
	if nav.View() != "root" {
		t.Fatalf("after pop view = %q, want root", nav.View())
	}

	// Pop the last screen → quit command.
	_, cmd := nav.Update(PopMsg{})
	if cmd == nil {
		t.Fatal("popping the last screen should return a quit command")
	}
}

func TestTheme_NoColorHasNoANSI(t *testing.T) {
	th := NewTheme(true)
	rendered := th.Title.Render("hello") + th.Selected.Render("world")
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("NO_COLOR theme emitted ANSI escapes: %q", rendered)
	}
	if !strings.Contains(rendered, "hello") || !strings.Contains(rendered, "world") {
		t.Fatalf("NO_COLOR theme dropped content: %q", rendered)
	}
}

func TestPrompter_AskAndConfirm(t *testing.T) {
	in := strings.NewReader("custom\n\ny\n")
	var out strings.Builder
	p := NewPrompter(in, &out)

	// First Ask: user types "custom".
	got, err := p.Ask("name", "default")
	if err != nil || got != "custom" {
		t.Fatalf("Ask = %q, %v; want custom", got, err)
	}
	// Second Ask: empty line → default.
	got, err = p.Ask("name", "default")
	if err != nil || got != "default" {
		t.Fatalf("Ask empty = %q, %v; want default", got, err)
	}
	// Confirm: "y".
	ok, err := p.Confirm("proceed", false)
	if err != nil || !ok {
		t.Fatalf("Confirm = %v, %v; want true", ok, err)
	}
}

// quitOnKey is a model that quits when any key is pressed, rendering a
// prompt. Drives the teatest program-runner check.
type quitOnKey struct{ done bool }

func (m quitOnKey) Init() tea.Cmd { return nil }
func (m quitOnKey) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}
func (m quitOnKey) View() string {
	if m.done {
		return "done"
	}
	return "press any key"
}

func TestProgramRun_DrivesModelToCompletion(t *testing.T) {
	// Feed a single keypress through Run's input and confirm the program
	// reaches the done state and returns its final model. Uses the real
	// Run() (WithInput/WithOutput/WithContext) rather than a test harness.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	in := strings.NewReader("\r")
	var out strings.Builder
	final, err := Run(ctx, quitOnKey{}, in, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !final.(quitOnKey).done {
		t.Fatal("model did not reach done state")
	}
}
