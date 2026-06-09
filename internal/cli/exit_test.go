package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestMapExit(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, exitOK},
		{"plain error is usage failure", errors.New("boom"), exitUsage},
		{"wrapped plain error is usage failure", fmt.Errorf("ctx: %w", errors.New("boom")), exitUsage},
		{"exit coder uses its code", &exitError{code: 1, err: errors.New("x")}, exitFailure},
		{"exit coder code 2", &exitError{code: 2, err: errors.New("x")}, exitUsage},
		{"missing flag uses usage code", &MissingFlagError{Flag: "--source"}, exitUsage},
		{"wrapped exit coder is unwrapped", fmt.Errorf("wrap: %w", &exitError{code: 1, err: errors.New("y")}), exitFailure},
		{"exit coder reporting 0 on a non-nil error is usage", &exitError{code: 0, err: errors.New("z")}, exitUsage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MapExit(c.err); got != c.want {
				t.Fatalf("MapExit(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

func TestMissingFlagError(t *testing.T) {
	e := &MissingFlagError{Flag: "--source", Why: "canonical repo URL"}
	if e.ExitCode() != exitUsage {
		t.Fatalf("ExitCode = %d, want %d", e.ExitCode(), exitUsage)
	}
	if got := e.Error(); got == "" || !contains(got, "--source") || !contains(got, "canonical repo URL") {
		t.Fatalf("Error() = %q, want it to name the flag and reason", got)
	}
	bare := &MissingFlagError{Flag: "--target"}
	if got := bare.Error(); !contains(got, "--target") {
		t.Fatalf("Error() = %q, want it to name the flag", got)
	}
}

func TestRequireFlag(t *testing.T) {
	// Provided: never errors.
	if err := requireFlag(true, true, "--source", "why"); err != nil {
		t.Fatalf("provided+nonInteractive should not error: %v", err)
	}
	// Interactive + missing: no error (a prompt would handle it).
	if err := requireFlag(false, false, "--source", "why"); err != nil {
		t.Fatalf("interactive+missing should not error: %v", err)
	}
	// Non-interactive + missing: MissingFlagError.
	err := requireFlag(true, false, "--source", "why")
	var mfe *MissingFlagError
	if !errors.As(err, &mfe) {
		t.Fatalf("non-interactive+missing should return MissingFlagError, got %v", err)
	}
	if mfe.Flag != "--source" {
		t.Fatalf("flag = %q, want --source", mfe.Flag)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
