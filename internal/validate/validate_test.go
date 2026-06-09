package validate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/internal/engine"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		plan engine.PlanResult
		want int
	}{
		{"no drift", engine.PlanResult{DriftDetected: false}, ExitNoDrift},
		{"drift", engine.PlanResult{DriftDetected: true}, ExitDrift},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.plan); got != tt.want {
				t.Fatalf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRenderText_NoDrift(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderText(&buf, engine.PlanResult{DriftDetected: false}); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if got := buf.String(); got != "No drift: all targets are up to date.\n" {
		t.Fatalf("unexpected no-drift output: %q", got)
	}
}

func TestRenderText_AllListsAndOrder(t *testing.T) {
	plan := engine.PlanResult{
		DriftDetected: true,
		Targets: []engine.TargetChange{{
			Target:      "claude",
			WouldCreate: []string{"a.md"},
			WouldUpdate: []string{"b.md"},
			WouldDelete: []string{"c.md"},
			OutOfBand:   []string{"d.md"},
			Warnings:    []string{"lossy translation"},
		}},
	}
	var buf bytes.Buffer
	if err := RenderText(&buf, plan); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()

	// Header present.
	if !strings.Contains(out, "target claude:") {
		t.Fatalf("missing target header: %q", out)
	}
	// Each labeled line present, in the documented order.
	wantOrder := []string{
		"create: a.md",
		"update: b.md",
		"delete: c.md",
		"out-of-band-modified: d.md",
		"warning: lossy translation",
	}
	last := -1
	for _, want := range wantOrder {
		idx := strings.Index(out, want)
		if idx < 0 {
			t.Fatalf("missing line %q in:\n%s", want, out)
		}
		if idx < last {
			t.Fatalf("line %q out of order in:\n%s", want, out)
		}
		last = idx
	}
}

func TestRenderText_ErrorSkipsLists(t *testing.T) {
	plan := engine.PlanResult{
		DriftDetected: true,
		Targets: []engine.TargetChange{{
			Target:      "cursor",
			Error:       "adapter exited 1",
			WouldCreate: []string{"should-not-render.md"},
		}},
	}
	var buf bytes.Buffer
	if err := RenderText(&buf, plan); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "error: adapter exited 1") {
		t.Fatalf("missing error line: %q", out)
	}
	if strings.Contains(out, "should-not-render.md") {
		t.Fatalf("list rendered despite error (continue path not taken): %q", out)
	}
}

func TestRenderText_MultipleTargets(t *testing.T) {
	plan := engine.PlanResult{
		DriftDetected: true,
		Targets: []engine.TargetChange{
			{Target: "claude", WouldCreate: []string{"a.md"}},
			{Target: "cursor", WouldUpdate: []string{"b.md"}},
		},
	}
	var buf bytes.Buffer
	if err := RenderText(&buf, plan); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "target claude:") || !strings.Contains(out, "target cursor:") {
		t.Fatalf("both target headers expected: %q", out)
	}
}

func TestMarshalJSON_ShapeAndNonNil(t *testing.T) {
	plan := engine.PlanResult{
		WorkspacePath: "/ws",
		Commit:        "abc123",
		DriftDetected: true,
		// All slices nil — must serialize as [] not null.
		Targets: []engine.TargetChange{{Target: "claude"}},
	}
	raw, err := MarshalJSON(plan)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		Workspace     string `json:"workspace"`
		Commit        string `json:"commit"`
		DriftDetected bool   `json:"drift_detected"`
		Targets       []struct {
			Target      string   `json:"target"`
			WouldCreate []string `json:"would_create"`
			WouldUpdate []string `json:"would_update"`
			WouldDelete []string `json:"would_delete"`
			OutOfBand   []string `json:"out_of_band_modified"`
			Warnings    []string `json:"warnings"`
			Error       string   `json:"error"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, raw)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", doc.SchemaVersion, SchemaVersion)
	}
	if doc.Workspace != "/ws" || doc.Commit != "abc123" || !doc.DriftDetected {
		t.Fatalf("top-level fields not carried: %+v", doc)
	}
	tgt := doc.Targets[0]
	for name, got := range map[string][]string{
		"would_create": tgt.WouldCreate,
		"would_update": tgt.WouldUpdate,
		"would_delete": tgt.WouldDelete,
		"out_of_band":  tgt.OutOfBand,
		"warnings":     tgt.Warnings,
	} {
		if got == nil {
			t.Fatalf("%s serialized as null, want [] (nonNil)", name)
		}
	}
	// error omitted when empty.
	if strings.Contains(string(raw), `"error"`) {
		t.Fatalf("empty error should be omitted: %s", raw)
	}
}

func TestMarshalJSON_ErrorPresentWhenSet(t *testing.T) {
	plan := engine.PlanResult{
		Targets: []engine.TargetChange{{Target: "claude", Error: "boom"}},
	}
	raw, err := MarshalJSON(plan)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !strings.Contains(string(raw), `"error": "boom"`) {
		t.Fatalf("expected error field present: %s", raw)
	}
}
