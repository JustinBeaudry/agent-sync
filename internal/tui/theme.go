package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds the lipgloss styles the wizard screens share. When NoColor
// is set, every style is the zero (plain) style so output carries no ANSI
// escapes — honoring NO_COLOR and keeping accessible/dumb terminals clean.
type Theme struct {
	NoColor  bool
	Title    lipgloss.Style
	Prompt   lipgloss.Style
	Selected lipgloss.Style
	Help     lipgloss.Style
	Error    lipgloss.Style
}

// NewTheme builds the shared theme. noColor collapses every style to plain
// text.
func NewTheme(noColor bool) Theme {
	if noColor {
		plain := lipgloss.NewStyle()
		return Theme{
			NoColor:  true,
			Title:    plain,
			Prompt:   plain,
			Selected: plain,
			Help:     plain,
			Error:    plain,
		}
	}
	return Theme{
		Title:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		Prompt:   lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		Help:     lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("8")),
		Error:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
	}
}
