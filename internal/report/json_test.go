package report

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMarshalJSON_StableSchema(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "deadbeef", "2026-06-08T00:00:00Z", ModeAtomic, []TargetReport{
		{Target: "claude", Status: StatusOK, Counts: Counts{Written: 3}, DurationMs: 12, Paths: []string{".claude/rules/aienvs/a.mdc"}},
	})
	data, err := MarshalJSON(s)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["schema_version"].(float64) != float64(SummarySchemaVersion) {
		t.Errorf("schema_version = %v", doc["schema_version"])
	}
	for _, k := range []string{"workspace", "commit", "generated_at", "mode", "targets", "summary"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("missing top-level key %q", k)
		}
	}
}

func TestMarshalJSON_EmptyTargetsIsArrayNotNull(t *testing.T) {
	t.Parallel()
	s := Summarize("/ws", "c", "T", ModeAtomic, nil)
	data, _ := MarshalJSON(s)
	if !bytes.Contains(data, []byte(`"targets": []`)) {
		t.Errorf("empty targets should serialize as []:\n%s", data)
	}
}

func TestMarshalJSON_Deterministic(t *testing.T) {
	t.Parallel()
	mk := func() []byte {
		s := Summarize("/ws", "c", "T", ModeBestEffort, []TargetReport{
			{Target: "cursor", Status: StatusOK},
			{Target: "claude", Status: StatusFailed, Error: "x"},
		})
		d, _ := MarshalJSON(s)
		return d
	}
	if !bytes.Equal(mk(), mk()) {
		t.Error("MarshalJSON not deterministic for identical inputs")
	}
}
