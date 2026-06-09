package sync

import (
	"errors"
	"testing"
)

// Platform-agnostic classification behavior. The errno→sentinel mapping
// itself is platform-specific (Unix errnos vs Windows error codes), so
// those cases live in errors_unix_test.go and errors_windows_test.go.
func TestClassifyRenameError_Agnostic(t *testing.T) {
	t.Parallel()

	if got := classifyRenameError(nil); got != nil {
		t.Errorf("nil in → %v", got)
	}

	// An error carrying no syscall.Errno is returned unchanged, never
	// classified to a sentinel.
	plain := errors.New("boom")
	got := classifyRenameError(plain)
	for _, s := range []error{ErrCrossVolume, ErrPermission, ErrLocked, ErrStale} {
		if errors.Is(got, s) {
			t.Errorf("non-errno error wrongly classified to %v", s)
		}
	}
	//nolint:errorlint // asserting the identical error value is returned unwrapped
	if got != plain {
		t.Errorf("non-errno error should be returned unchanged")
	}
}
