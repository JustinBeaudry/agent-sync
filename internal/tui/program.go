// Package tui holds the Bubble Tea + Bubbles interactive layer: a program
// runner, a screen-stack navigator, a NO_COLOR-aware theme, and an
// accessible linear fallback. It is built on the v1 charm line
// (github.com/charmbracelet/bubbletea, .../bubbles, .../lipgloss).
//
// The package never writes to stdout: programs render to the writer the
// caller supplies (the CLI passes stderr/the TTY), so a piped
// --output=json stream stays clean even when a wizard runs (KTD-6).
package tui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// Interactive reports whether a full Bubble Tea wizard may run. It is the
// single entry precondition: a TTY, not forced non-interactive, and not in
// accessible (screen-reader) mode. Callers pass the resolved access
// signals; the predicate is decoupled from the cli package to avoid an
// import cycle.
func Interactive(isTTY, nonInteractive, accessible bool) bool {
	return isTTY && !nonInteractive && !accessible
}

// Run executes a Bubble Tea program against the supplied IO and context.
// Output goes to out (the caller passes stderr or the TTY, never stdout).
// The returned model is the final state, from which the caller reads the
// wizard's collected result.
func Run(ctx context.Context, model tea.Model, in io.Reader, out io.Writer) (tea.Model, error) {
	p := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(in),
		tea.WithOutput(out),
	)
	return p.Run()
}
