package cli

import (
	"os"
	"testing"
)

func TestResolveAccess(t *testing.T) {
	cases := []struct {
		name string
		in   accessInput
		want Access
	}{
		{
			name: "interactive tty defaults to text + color",
			in:   accessInput{stdinTTY: true, stdoutTTY: true},
			want: Access{IsTTY: true, NoColor: false, Accessible: false, NonInteractive: false, Output: OutputText},
		},
		{
			name: "piped stdout defaults to json + no color + non-interactive",
			in:   accessInput{stdinTTY: false, stdoutTTY: false},
			want: Access{IsTTY: false, NoColor: true, Accessible: false, NonInteractive: true, Output: OutputJSON},
		},
		{
			name: "NO_COLOR suppresses color on a tty",
			in:   accessInput{stdinTTY: true, stdoutTTY: true, noColorEnv: true},
			want: Access{IsTTY: true, NoColor: true, Output: OutputText},
		},
		{
			name: "FORCE_COLOR overrides NO_COLOR and non-tty",
			in:   accessInput{stdinTTY: false, stdoutTTY: false, noColorEnv: true, forceColorEnv: true},
			want: Access{IsTTY: false, NoColor: false, NonInteractive: true, Output: OutputJSON},
		},
		{
			name: "TERM=dumb is accessible",
			in:   accessInput{stdinTTY: true, stdoutTTY: true, termDumb: true},
			want: Access{IsTTY: true, Accessible: true, Output: OutputText},
		},
		{
			name: "AGENT_SYNC_ACCESSIBLE is accessible",
			in:   accessInput{stdinTTY: true, stdoutTTY: true, accessibleEnv: true},
			want: Access{IsTTY: true, Accessible: true, Output: OutputText},
		},
		{
			name: "explicit --output=json on a tty wins over text default",
			in:   accessInput{stdinTTY: true, stdoutTTY: true, outputFlag: "json"},
			want: Access{IsTTY: true, Output: OutputJSON},
		},
		{
			name: "explicit --output=text when piped wins over json default",
			in:   accessInput{stdinTTY: false, stdoutTTY: false, outputFlag: "TEXT"},
			want: Access{IsTTY: false, NoColor: true, NonInteractive: true, Output: OutputText},
		},
		{
			name: "--non-interactive flag forces non-interactive even on a tty",
			in:   accessInput{stdinTTY: true, stdoutTTY: true, nonInteractiveFlag: true},
			want: Access{IsTTY: true, NonInteractive: true, Output: OutputText},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveAccess(c.in)
			if got != c.want {
				t.Fatalf("resolveAccess(%+v)\n got = %+v\nwant = %+v", c.in, got, c.want)
			}
		})
	}
}

func TestIsTerminal_DevNullIsNotATTY(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Fatal("/dev/null must not be treated as a TTY")
	}
}

func TestIsTerminal_NilAndNonFileAreSafe(t *testing.T) {
	var nilFile *os.File
	if isTerminal(nilFile) {
		t.Fatal("typed-nil *os.File should not be a TTY (and must not panic)")
	}
	if isTerminal("not a file") {
		t.Fatal("non-file should not be a TTY")
	}
}

func TestIsTruthy(t *testing.T) {
	for _, s := range []string{"1", "true", "TRUE", "yes", "on"} {
		if !isTruthy(s) {
			t.Errorf("isTruthy(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "0", "false", "no", "off", "maybe"} {
		if isTruthy(s) {
			t.Errorf("isTruthy(%q) = true, want false", s)
		}
	}
}
