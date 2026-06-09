package cli

import "fmt"

// MissingFlagError is returned when a command needs a value that would
// normally come from an interactive prompt, but --non-interactive is set
// (or stdin is not a TTY) so prompting is forbidden. It names the exact
// flag and why, and carries the documented exit code so the root maps it
// to a deterministic non-zero exit (AGENTS invariant #3: non-interactive
// mode never prompts, never hangs).
type MissingFlagError struct {
	Flag string // the flag the user must pass, e.g. "--source"
	Why  string // what the value is for
}

func (e *MissingFlagError) Error() string {
	if e.Why == "" {
		return fmt.Sprintf("required flag %s is missing in non-interactive mode", e.Flag)
	}
	return fmt.Sprintf("required flag %s is missing in non-interactive mode (%s)", e.Flag, e.Why)
}

// ExitCode reports the exit status for a missing required flag.
func (e *MissingFlagError) ExitCode() int { return exitUsage }

// requireFlag returns a MissingFlagError when non-interactive mode is
// active and a needed value was not supplied. Commands call this at the
// point they would otherwise prompt.
func requireFlag(nonInteractive, provided bool, flag, why string) error {
	if provided || !nonInteractive {
		return nil
	}
	return &MissingFlagError{Flag: flag, Why: why}
}
