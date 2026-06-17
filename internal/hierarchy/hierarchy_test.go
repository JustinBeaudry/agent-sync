package hierarchy

import (
	"encoding/json"
	"testing"
)

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

// TestLevelMarshalJSON pins that a Level marshals as its lowercase label (a
// quoted string) rather than the underlying integer, so a Level embedded in
// JSON (e.g. coverage warnings) is consistent with the string `level` fields
// the CLI emits via Level.String().
func TestLevelMarshalJSON(t *testing.T) {
	got, err := json.Marshal(LevelDirectory)
	if err != nil {
		t.Fatalf("json.Marshal(LevelDirectory): %v", err)
	}
	if want := `"directory"`; string(got) != want {
		t.Errorf("json.Marshal(LevelDirectory) = %s, want %s", got, want)
	}
}
