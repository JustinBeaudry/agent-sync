package conformance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aienvs/aienvs/internal/adapter"
	"github.com/aienvs/aienvs/internal/adapter/contract"
)

const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusSkip = "skip"
)

// RunOptions controls the conformance harness.
type RunOptions struct {
	Cases    []Case
	Timeouts adapter.SubprocessTimeouts
	Verbose  bool
}

// Report is the per-run result set.
type Report struct {
	Cases   []CaseResult `json:"cases"`
	Summary Summary      `json:"summary"`
}

// Summary is the rollup count for a report.
type Summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// CaseResult is one case's outcome.
type CaseResult struct {
	Name        string              `json:"name"`
	Status      string              `json:"status"`
	Reason      string              `json:"reason"`
	ExpectedOps []contract.OpRecord `json:"expected_ops"`
	ActualOps   []contract.OpRecord `json:"actual_ops"`
	MissingOps  []contract.OpRecord `json:"missing_ops,omitempty"`
	ExtraOps    []contract.OpRecord `json:"extra_ops,omitempty"`
}

// Run executes the supplied adapter binary against the corpus.
func Run(ctx context.Context, binary string, opts RunOptions) (Report, error) {
	var report Report

	cases := opts.Cases
	if cases == nil {
		loaded, err := LoadCorpus()
		if err != nil {
			return report, err
		}
		cases = loaded
	}
	if len(cases) == 0 {
		return report, nil
	}

	for _, tc := range cases {
		if err := ctx.Err(); err != nil {
			report.Summary = tallySummary(report.Cases)
			return report, fmt.Errorf("conformance: run aborted before case %q: %w", tc.Name, err)
		}

		result, err := runCase(ctx, binary, opts, tc)
		if err != nil {
			return report, err
		}
		report.Cases = append(report.Cases, result)
	}

	report.Summary = tallySummary(report.Cases)

	return report, nil
}

func runCase(ctx context.Context, binary string, opts RunOptions, tc Case) (result CaseResult, err error) {
	if tc.Expect.Kind == ExpectedKindSkip {
		return CaseResult{Name: tc.Name, Status: StatusSkip, Reason: tc.Expect.Skip}, nil
	}

	a := &adapter.Adapter{
		Source: adapter.SourcePATH,
		Manifest: adapter.AdapterManifest{
			Name:            "conformance-target",
			ContractVersion: adapter.ContractVersionV1,
			Command:         []string{binary},
			ReservedPrefix:  caseReservedPrefix(tc),
		},
	}

	session, err := a.NewSession(ctx, adapter.SessionOptions{
		WorkspaceRoot: "/tmp/aienvs-conformance",
		IRVersion:     "v1",
		Timeouts:      opts.Timeouts,
	})
	if err != nil {
		spawnFailure := CaseResult{
			Name:   tc.Name,
			Status: StatusFail,
			Reason: fmt.Sprintf("session spawn failed: %v", err),
		}
		if tc.Expect.Kind == ExpectedKindOps {
			spawnFailure.ExpectedOps = append([]contract.OpRecord(nil), tc.Expect.Ops...)
			spawnFailure.ActualOps = []contract.OpRecord{}
		}
		return spawnFailure, nil
	}
	var actualOps []contract.OpRecord
	finalized := false
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveShutdownTimeout(opts.Timeouts))
		defer cancel()
		shutdownErr := session.Shutdown(shutdownCtx)
		if finalized {
			return
		}
		result = classifyCaseResult(tc, actualOps, shutdownErr)
	}()

	initResult, err := session.Initialize(ctx)
	if err != nil {
		finalized = true
		return classifyCaseResult(tc, nil, err), nil
	}

	if ok, reason := caseSupportedByAdapter(tc, initResult); !ok {
		finalized = true
		return CaseResult{Name: tc.Name, Status: StatusSkip, Reason: reason}, nil
	}

	if err := session.Initialized(ctx); err != nil {
		finalized = true
		return classifyCaseResult(tc, nil, err), nil
	}

	emitResult, err := session.Emit(ctx, tc.Name, tc.IR)
	if err != nil {
		finalized = true
		return classifyCaseResult(tc, nil, err), nil
	}

	actualOps = emitResult.OpsPerformed
	return CaseResult{}, nil
}

func classifyCaseResult(tc Case, actualOps []contract.OpRecord, err error) CaseResult {
	result := CaseResult{Name: tc.Name, Reason: ""}

	switch tc.Expect.Kind {
	case ExpectedKindOps:
		result.ExpectedOps = append([]contract.OpRecord(nil), tc.Expect.Ops...)
		if actualOps == nil {
			result.ActualOps = []contract.OpRecord{}
		} else {
			result.ActualOps = append([]contract.OpRecord(nil), actualOps...)
		}
		if err != nil {
			result.Status = StatusFail
			result.Reason = fmt.Sprintf("unexpected runtime error: %v", err)
			return result
		}
		ok, missing, extra := MatchOps(tc.Expect.Ops, actualOps, tc.Expect.StrictOrder)
		if ok {
			result.Status = StatusPass
			return result
		}
		result.Status = StatusFail
		result.MissingOps = missing
		result.ExtraOps = extra
		result.Reason = fmt.Sprintf("ops mismatch: missing=%v extra=%v", missing, extra)
		return result
	case ExpectedKindError:
		if actualOps != nil {
			result.ActualOps = append([]contract.OpRecord(nil), actualOps...)
		}
		if MatchError(tc.Expect.Error, err) {
			result.Status = StatusPass
			return result
		}
		result.Status = StatusFail
		if err == nil {
			result.Reason = fmt.Sprintf("expected error %s, got success", tc.Expect.Error)
			return result
		}
		result.Reason = fmt.Sprintf("expected error %s, got %v", tc.Expect.Error, err)
		return result
	default:
		result.Status = StatusSkip
		result.Reason = tc.Expect.Skip
		return result
	}
}

func tallySummary(results []CaseResult) Summary {
	summary := Summary{Total: len(results)}
	for _, result := range results {
		switch result.Status {
		case StatusPass:
			summary.Passed++
		case StatusFail:
			summary.Failed++
		case StatusSkip:
			summary.Skipped++
		}
	}
	return summary
}

func caseSupportedByAdapter(tc Case, initResult *contract.InitializeResult) (bool, string) {
	for kind, want := range tc.Manifest.Capabilities.ConceptKinds {
		have, ok := initResult.Capabilities.ConceptKinds[kind]
		if !ok || have != want {
			return false, fmt.Sprintf("adapter declared %q as %q, fixture requires %q", kind, have, want)
		}
	}
	for _, want := range tc.Manifest.DeclaredOutputs {
		if !declaredOutputPresent(initResult.DeclaredOutputs, want) {
			return false, fmt.Sprintf("adapter did not declare required output %q", want.Path)
		}
	}
	return true, ""
}

func declaredOutputPresent(actual []contract.DeclaredOutput, want contract.DeclaredOutput) bool {
	for _, got := range actual {
		if got.Path == want.Path && got.Mode == want.Mode {
			return true
		}
	}
	return false
}

func caseReservedPrefix(tc Case) string {
	if len(tc.Manifest.DeclaredOutputs) == 0 {
		return ""
	}
	path := tc.Manifest.DeclaredOutputs[0].Path
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		return path[:idx]
	}
	return path
}

func resolveShutdownTimeout(t adapter.SubprocessTimeouts) time.Duration {
	if t.Shutdown != 0 {
		return t.Shutdown
	}
	return adapter.DefaultSubprocessTimeouts().Shutdown
}
