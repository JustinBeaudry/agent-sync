package hierarchy

import "testing"

func TestLevelString(t *testing.T) {
	cases := []struct {
		level Level
		want  string
	}{
		{LevelUser, "user"},
		{LevelProject, "project"},
		{LevelDirectory, "directory"},
		{Level(99), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.level.String(); got != tc.want {
				t.Errorf("Level(%d).String() = %q, want %q", tc.level, got, tc.want)
			}
		})
	}
}
