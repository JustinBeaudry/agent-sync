package cli

import (
	"io"
	"os"
	"strings"
)

// OutputFormat is the resolved data-output shape for a command.
type OutputFormat string

const (
	// OutputText is human-readable output (the default on a TTY).
	OutputText OutputFormat = "text"
	// OutputJSON is machine-readable output (the default when stdout is
	// piped, or when --output=json is set).
	OutputJSON OutputFormat = "json"
)

// Access is the resolved interactivity + presentation context for one
// command invocation. It is the single source of truth the engine, the
// report renderer, and any TUI entry consult — TTY/NO_COLOR/output-format
// detection lives here and nowhere else (KTD-5).
type Access struct {
	// IsTTY is true when both stdin and stdout are character devices.
	IsTTY bool
	// NoColor is true when color must be suppressed (NO_COLOR set, or not
	// a TTY, unless FORCE_COLOR overrides).
	NoColor bool
	// Accessible is true when a screen-reader-friendly linear flow should
	// be used instead of a full TUI (TERM=dumb or AIENVS_ACCESSIBLE=1).
	Accessible bool
	// NonInteractive is true when --non-interactive/--yes was set, or when
	// stdin is not a TTY. Gates every interactive prompt.
	NonInteractive bool
	// Output is the resolved data-output format.
	Output OutputFormat
}

// accessInput carries the raw signals access resolution reads, so the
// logic is testable without real file descriptors or env mutation.
type accessInput struct {
	stdinTTY           bool
	stdoutTTY          bool
	noColorEnv         bool // NO_COLOR present (any value)
	forceColorEnv      bool // FORCE_COLOR present (any value)
	termDumb           bool // TERM=dumb
	accessibleEnv      bool // AIENVS_ACCESSIBLE truthy
	nonInteractiveFlag bool
	outputFlag         string // "", "text", or "json"
}

// resolveAccess computes the Access context from raw signals. Precedence:
//   - NonInteractive: flag OR stdin-not-a-TTY.
//   - Output: explicit flag wins; else json when stdout is not a TTY, text otherwise.
//   - NoColor: NO_COLOR or non-TTY suppresses, unless FORCE_COLOR forces.
//   - Accessible: TERM=dumb or AIENVS_ACCESSIBLE.
func resolveAccess(in accessInput) Access {
	a := Access{
		IsTTY:          in.stdinTTY && in.stdoutTTY,
		Accessible:     in.termDumb || in.accessibleEnv,
		NonInteractive: in.nonInteractiveFlag || !in.stdinTTY,
	}

	switch strings.ToLower(strings.TrimSpace(in.outputFlag)) {
	case string(OutputJSON):
		a.Output = OutputJSON
	case string(OutputText):
		a.Output = OutputText
	default:
		if in.stdoutTTY {
			a.Output = OutputText
		} else {
			a.Output = OutputJSON
		}
	}

	switch {
	case in.forceColorEnv:
		a.NoColor = false
	case in.noColorEnv || !in.stdoutTTY:
		a.NoColor = true
	default:
		a.NoColor = false
	}
	return a
}

// ResolveAccess builds the Access context from the process environment and
// the given stdin/stdout. nonInteractiveFlag and outputFlag come from the
// resolved persistent flags.
func ResolveAccess(in io.Reader, out io.Writer, nonInteractiveFlag bool, outputFlag string) Access {
	_, noColor := os.LookupEnv("NO_COLOR")
	_, forceColor := os.LookupEnv("FORCE_COLOR")
	term := os.Getenv("TERM")
	return resolveAccess(accessInput{
		stdinTTY:           isTerminal(in),
		stdoutTTY:          isTerminal(out),
		noColorEnv:         noColor,
		forceColorEnv:      forceColor,
		termDumb:           term == "dumb",
		accessibleEnv:      isTruthy(os.Getenv("AIENVS_ACCESSIBLE")),
		nonInteractiveFlag: nonInteractiveFlag,
		outputFlag:         outputFlag,
	})
}

// isTerminal reports whether v is an *os.File backed by a character
// device (a TTY). Non-file readers/writers (buffers, pipes) are not TTYs.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
