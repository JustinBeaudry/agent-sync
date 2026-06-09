package conformance

import (
	"errors"
	"io"
	"slices"
	"strings"
	"syscall"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

func MatchOps(expected, actual []contract.OpRecord, strictOrder bool) (bool, []contract.OpRecord, []contract.OpRecord) {
	if strictOrder {
		return matchOpsOrdered(expected, actual)
	}
	return matchOpsUnordered(expected, actual)
}

func matchOpsOrdered(expected, actual []contract.OpRecord) (bool, []contract.OpRecord, []contract.OpRecord) {
	limit := min(len(expected), len(actual))
	missing := make([]contract.OpRecord, 0)
	extra := make([]contract.OpRecord, 0)

	for i := 0; i < limit; i++ {
		if expected[i] != actual[i] {
			missing = append(missing, expected[i])
			extra = append(extra, actual[i])
		}
	}
	if len(expected) > limit {
		missing = append(missing, expected[limit:]...)
	}
	if len(actual) > limit {
		extra = append(extra, actual[limit:]...)
	}
	return len(missing) == 0 && len(extra) == 0, missing, extra
}

func matchOpsUnordered(expected, actual []contract.OpRecord) (bool, []contract.OpRecord, []contract.OpRecord) {
	missing := make([]contract.OpRecord, 0)
	extra := slices.Clone(actual)

	for _, want := range expected {
		idx := slices.Index(extra, want)
		if idx == -1 {
			missing = append(missing, want)
			continue
		}
		extra = slices.Delete(extra, idx, idx+1)
	}

	return len(missing) == 0 && len(extra) == 0, missing, extra
}

func MatchError(expected string, err error) bool {
	if err == nil {
		return false
	}

	switch expected {
	case "ErrAdapterCookieMissing":
		return errors.Is(err, adapter.ErrAdapterCookieMissing)
	case "ErrAdapterCookieMismatch":
		return errors.Is(err, adapter.ErrAdapterCookieMismatch)
	case "ErrAdapterProtocolMismatch":
		return errors.Is(err, adapter.ErrAdapterProtocolMismatch)
	case "ErrAdapterUndeclaredOutput":
		return errors.Is(err, adapter.ErrAdapterUndeclaredOutput)
	case "ErrAdapterProtocolOrderViolation":
		return errors.Is(err, adapter.ErrAdapterProtocolOrderViolation)
	case "ErrAdapterCapabilityLied":
		return errors.Is(err, adapter.ErrAdapterCapabilityLied)
	case "ErrAdapterTimeout":
		return errors.Is(err, adapter.ErrAdapterTimeout)
	case "ErrFrameTooLarge":
		return errors.Is(err, contract.ErrFrameTooLarge)
	case "SubprocessExitError":
		var exitErr *adapter.SubprocessExitError
		if errors.As(err, &exitErr) {
			return true
		}
		// A subprocess that exits abnormally surfaces in one of two
		// timing-dependent ways: the read side gets EOF, or — when the
		// child dies before/while the harness writes the init frame — the
		// write side gets a broken/closed pipe. Both mean the subprocess
		// died; accept either so the assertion is timing-independent.
		return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
			isBrokenPipe(err)
	default:
		return false
	}
}

// isBrokenPipe reports whether err is a write-to-a-dead-subprocess pipe
// failure, cross-platform. errors.Is(syscall.EPIPE) covers Unix and the
// Go-mapped Windows case; the string checks catch the Windows pipe-closed
// messages (ERROR_BROKEN_PIPE / ERROR_NO_DATA) that aren't always mapped
// to EPIPE.
func isBrokenPipe(err error) bool {
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "pipe is being closed") ||
		strings.Contains(msg, "pipe has been ended")
}
