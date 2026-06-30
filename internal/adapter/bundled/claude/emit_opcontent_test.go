package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// TestEmit_OpsCarryContent proves the U0 op-content channel: handleEmit
// populates EmitResult.Ops with full op envelopes that decode (via the
// frozen contract.DecodeOp) back into typed ops with their content
// intact, in the same order as the OpsPerformed summary. This is what
// lets the CLI core (engine, U1) perform the actual writes.
func TestEmit_OpsCarryContent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "ir", "rule-only.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	res, err := handleEmit(context.Background(), adapterkit.EmitParams{
		Target: "claude",
		IR:     json.RawMessage(raw),
	}, "project")
	if err != nil {
		t.Fatalf("handleEmit: %v", err)
	}

	if len(res.Ops) == 0 {
		t.Fatal("EmitResult.Ops is empty — op content was not transmitted")
	}
	if len(res.Ops) != len(res.OpsPerformed) {
		t.Fatalf("Ops/OpsPerformed length mismatch: %d vs %d", len(res.Ops), len(res.OpsPerformed))
	}

	sawWriteWithContent := false
	for i, rawOp := range res.Ops {
		op, err := contract.DecodeOp(rawOp)
		if err != nil {
			t.Fatalf("DecodeOp[%d]: %v", i, err)
		}
		// Order parity with the summary.
		if got, want := string(op.OpKind()), string(res.OpsPerformed[i].Op); got != want {
			t.Fatalf("op[%d] kind: got %q want %q", i, got, want)
		}
		if got, want := op.OpPath(), res.OpsPerformed[i].Path; got != want {
			t.Fatalf("op[%d] path: got %q want %q", i, got, want)
		}
		if wf, ok := op.(contract.OpWriteFile); ok && len(wf.Content) > 0 {
			sawWriteWithContent = true
		}
	}

	if !sawWriteWithContent {
		t.Fatal("no write_file op carried content — engine could not perform writes")
	}
}

// TestEmitResult_BackwardCompatNoOpsField confirms an EmitResult JSON
// payload without the additive "ops" field still decodes cleanly (Ops
// nil), preserving the "grow capabilities additively" guarantee.
func TestEmitResult_BackwardCompatNoOpsField(t *testing.T) {
	var res contract.EmitResult
	if err := json.Unmarshal([]byte(`{"ops_performed":[{"op":"mkdir","path":".claude"}]}`), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Ops != nil {
		t.Fatalf("expected nil Ops for legacy payload, got %v", res.Ops)
	}
	if len(res.OpsPerformed) != 1 {
		t.Fatalf("expected 1 op_performed, got %d", len(res.OpsPerformed))
	}
}
