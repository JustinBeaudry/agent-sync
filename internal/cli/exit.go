package cli

import "errors"

// Documented process exit codes. Operational failures use exitError
// (from cmd_adapter.go) or these constants; the report verdict carries
// its own 0/1 in report.Outcome.ExitCode.
const (
	exitOK      = 0 // success
	exitFailure = 1 // operation completed but reported failure/drift
	exitUsage   = 2 // usage error, missing required flag, spawn failure
)

// exitCoder is any error that carries its own process exit code (the
// trust package, the adapter exitError, MissingFlagError all implement
// it). MapExit unwraps the chain looking for one.
type exitCoder interface{ ExitCode() int }

// MapExit translates an error returned from command execution into a
// process exit code. nil → 0. An error implementing ExitCode() (anywhere
// in its Unwrap chain) uses that code. Everything else is a generic
// failure (exitUsage), since an unclassified error at the command
// boundary is almost always a usage/operational problem rather than a
// clean "operation reported failure" signal.
func MapExit(err error) int {
	if err == nil {
		return exitOK
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		code := ec.ExitCode()
		// An ExitCode() of 0 on a non-nil error is a contradiction; treat
		// it as a generic failure rather than silently reporting success.
		if code == 0 {
			return exitUsage
		}
		return code
	}
	return exitUsage
}
