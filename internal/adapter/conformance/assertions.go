package conformance

import (
	"errors"
	"io"
	"slices"

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/adapter/contract"
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
		return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
	default:
		return false
	}
}

func isKnownExpectedError(name string) bool {
	switch name {
	case "ErrAdapterCookieMissing",
		"ErrAdapterCookieMismatch",
		"ErrAdapterProtocolMismatch",
		"ErrAdapterUndeclaredOutput",
		"ErrAdapterProtocolOrderViolation",
		"ErrAdapterCapabilityLied",
		"ErrAdapterTimeout",
		"ErrFrameTooLarge",
		"SubprocessExitError":
		return true
	default:
		return false
	}
}
