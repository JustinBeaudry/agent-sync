package trust

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Prompter renders minimal stdin-based trust prompts. The full huh/v2
// wizard surface lands in Unit 17; Prompter is the minimum needed to ship
// the trust CLI without pulling the TUI dependency forward.
//
// A Prompter is safe for single-threaded use. Callers that need concurrent
// prompts should construct one per goroutine.
type Prompter struct {
	in      *bufio.Reader
	out     io.Writer
	noColor bool
}

// NewPrompter wraps in as a buffered reader and returns a Prompter that
// writes to out. Both io.Reader and io.Writer are injectable for tests;
// callers typically pass os.Stdin and os.Stderr.
func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{
		in:  bufio.NewReader(in),
		out: out,
	}
}

// WithNoColor disables ANSI color output. Callers toggle this when
// NO_COLOR is set in the environment or when stderr isn't a TTY.
func (p *Prompter) WithNoColor() *Prompter {
	p.noColor = true
	return p
}

// ConfirmFirstURL prompts the user to accept a first-use URL at the
// resolved SHA. Returns (true, nil) on accept, (false, nil) on decline or
// EOF.
//
// Accepting answers: "y", "yes" (case-insensitive). Everything else
// including empty input, "n", and EOF declines. This asymmetry is the
// safer default: silent/errant input does not grant trust.
func (p *Prompter) ConfirmFirstURL(url, sha string) (bool, error) {
	p.write("New source detected.\n")
	p.write(fmt.Sprintf("  URL: %s\n", url))
	p.write(fmt.Sprintf("  SHA: %s\n", shortSHA(sha)))
	p.write("Trust this source at this SHA? [y/N] ")
	return p.readYesNo()
}

// ConfirmNewSHA prompts the user to accept a SHA update for a URL already
// present in their trust history. Returns (true, nil) on accept.
//
// This prompt is used by the interactive trust subcommands (add / promote).
// It is NEVER used during sync — plan decision #9 removes mid-sync prompts
// in favor of the pending-review flow.
func (p *Prompter) ConfirmNewSHA(url, newSHA, oldSHA string) (bool, error) {
	p.write(fmt.Sprintf("Source %s has a new SHA.\n", url))
	p.write(fmt.Sprintf("  old: %s\n", shortSHA(oldSHA)))
	p.write(fmt.Sprintf("  new: %s\n", shortSHA(newSHA)))
	p.write("Promote the new SHA? [y/N] ")
	return p.readYesNo()
}

// RenderRevokedBanner emits the SSH-style banner for a revoked URL and
// returns ErrRevokedTrustAnchor. It NEVER reads from stdin — the block is
// absolute, even on a TTY.
func (p *Prompter) RenderRevokedBanner(url string) error {
	p.write(p.color(colorRed, "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n"))
	p.write(p.color(colorRed, "@     WARNING: REVOKED TRUST ANCHOR REAPPEARED!           @\n"))
	p.write(p.color(colorRed, "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n"))
	p.write(fmt.Sprintf("The source %s was previously revoked.\n", url))
	p.write("aienvs refuses to proceed until you explicitly re-enable it.\n")
	p.write(fmt.Sprintf("\nRemediation:\n  aienvs trust reset %s\n", url))
	return fmt.Errorf("%w: %s", ErrRevokedTrustAnchor, url)
}

// TypedConfirmation requires the user to type a specific value (typically
// the URL or target name) before a destructive op proceeds. Used by
// `trust reset`, `trust revoke`, and (by the sync CLI) `--adopt-prefix`.
// Returns (true, nil) only when the trimmed input equals expect exactly.
//
// verb is a short description of the action for the prompt body (e.g.
// "revoke", "reset", "adopt").
func (p *Prompter) TypedConfirmation(verb, expect string) (bool, error) {
	p.write(fmt.Sprintf("This will %s %q.\n", verb, expect))
	p.write(fmt.Sprintf("To confirm, type exactly: %s\n> ", expect))

	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(line) == expect, nil
}

// readYesNo reads one line and classifies it. EOF and any non-affirmative
// answer are declines.
func (p *Prompter) readYesNo() (bool, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// write is a small helper for line-oriented output that mirrors fprintf
// semantics without returning the (always-ignored) byte count and error.
func (p *Prompter) write(s string) {
	_, _ = io.WriteString(p.out, s)
}

// Minimal ANSI color constants. We keep the palette tiny on purpose —
// Unit 17 will bring proper lipgloss styling.
const (
	colorRed   = "\x1b[31m"
	colorReset = "\x1b[0m"
)

func (p *Prompter) color(code, s string) string {
	if p.noColor {
		return s
	}
	return code + s + colorReset
}
