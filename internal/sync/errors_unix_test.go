//go:build unix

package sync

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestClassifyRenameError_Unix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		errno syscall.Errno
		want  error // sentinel, or nil = returned unchanged
	}{
		{"exdev", syscall.EXDEV, ErrCrossVolume},
		{"eacces", syscall.EACCES, ErrPermission},
		{"eperm", syscall.EPERM, ErrPermission},
		{"erofs", syscall.EROFS, ErrPermission},
		{"enoent unmapped", syscall.ENOENT, nil},
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
