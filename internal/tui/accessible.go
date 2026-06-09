package tui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Prompter drives a linear, screen-reader-friendly prompt loop used when
// accessible mode is active (TERM=dumb or AIENVS_ACCESSIBLE=1) instead of
// the full Bubble Tea TUI. It reads line-by-line from in and writes
// prompts to out (the caller passes stderr/the TTY, never stdout).
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// NewPrompter builds a linear prompter over the given IO.
func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out}
}

// Ask writes a prompt and returns the trimmed line the user typed. If the
// user enters nothing and def is non-empty, def is returned.
func (p *Prompter) Ask(prompt, def string) (string, error) {
	if def != "" {
		_, _ = fmt.Fprintf(p.out, "%s [%s]: ", prompt, def)
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", prompt)
	}
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// Confirm asks a yes/no question. def is the answer for an empty line.
func (p *Prompter) Confirm(prompt string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	_, _ = fmt.Fprintf(p.out, "%s [%s]: ", prompt, hint)
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return def, nil
	}
}
