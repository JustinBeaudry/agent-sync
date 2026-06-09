package sync

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestClassifyRenameError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    error
		want  error // sentinel, or nil meaning "returned unchanged"
		exact bool  // when want==nil, assert the original is returned
	}{
		{"nil", nil, nil, false},
		{"exdev", &os.LinkError{Op: "rename", Err: syscall.EXDEV}, ErrCrossVolume, false},
		{"eacces", &os.LinkError{Op: "rename", Err: syscall.EACCES}, ErrPermission, false},
		{"unmapped errno", &os.LinkError{Op: "rename", Err: syscall.ENOENT}, nil, true},
		{"non-errno", errors.New("boom"), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyRenameError(tc.in)
			switch {
			case tc.in == nil:
				if got != nil {
					t.Errorf("nil in → %v", got)
				}
			case tc.want != nil:
				if !errors.Is(got, tc.want) {
					t.Errorf("got %v want sentinel %v", got, tc.want)
				}
			case tc.exact:
				// Must be returned unchanged (not classified to a sentinel).
				for _, s := range []error{ErrCrossVolume, ErrPermission, ErrLocked, ErrStale} {
					if errors.Is(got, s) {
						t.Errorf("unmapped error wrongly classified to %v", s)
					}
				}
				//nolint:errorlint // asserting the identical error value is returned unwrapped
				if got != tc.in {
					t.Errorf("unmapped error should be returned unchanged")
				}
			}
		})
	}
}
