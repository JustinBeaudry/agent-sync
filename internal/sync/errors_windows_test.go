//go:build windows

package sync

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestClassifyRenameError_Windows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		errno syscall.Errno
		want  error // sentinel, or nil = returned unchanged
	}{
		{"not same device", errorNotSameDevice, ErrCrossVolume},
		{"sharing violation", errorSharingViolation, ErrLocked},
		{"access denied", errorAccessDenied, ErrLocked},
		{"file not found unmapped", syscall.Errno(2), nil}, // ERROR_FILE_NOT_FOUND
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &os.LinkError{Op: "rename", Err: tc.errno}
			got := classifyRenameError(in)
			if tc.want != nil {
				if !errors.Is(got, tc.want) {
					t.Errorf("got %v want %v", got, tc.want)
				}
				return
			}
			for _, s := range []error{ErrCrossVolume, ErrPermission, ErrLocked} {
				if errors.Is(got, s) {
					t.Errorf("unmapped errno classified to %v", s)
				}
			}
		})
	}
}
