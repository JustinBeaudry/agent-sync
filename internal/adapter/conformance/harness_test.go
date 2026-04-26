package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aienvs/aienvs/internal/adapter/contract"
)

var (
	conformanceTestBinaryDir     string
	conformanceTestBinaryDirOnce sync.Once
	conformanceTestBinaryDirErr  error

	echoBinaryOnce sync.Once
	echoBinaryPath string
	echoBinaryErr  error

	crashyBinaryOnce sync.Once
	crashyBinaryPath string
	crashyBinaryErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if conformanceTestBinaryDir != "" {
		_ = os.RemoveAll(conformanceTestBinaryDir)
	}
	os.Exit(code)
}

func TestRun_EmptyCorpus(t *testing.T) {
	t.Parallel()

	report, err := Run(context.Background(), "/does/not/matter", RunOptions{Cases: []Case{}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.Total != 0 || len(report.Cases) != 0 {
		t.Fatalf("report=%+v want empty", report)
	}
}

func TestRun_EchoBinaryPassesSupportedCaseAndSkipsUnsupportedCase(t *testing.T) {
	t.Parallel()

	echo := ensureEchoBinary(t)
	cases := mustCasesByName(t, "happy-rule", "happy-skill")

	report, err := Run(context.Background(), echo, RunOptions{Cases: cases})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Cases) != 2 {
		t.Fatalf("len(report.Cases)=%d want 2", len(report.Cases))
	}

	if report.Cases[0].Name != "happy-rule" || report.Cases[0].Status != StatusPass {
		t.Fatalf("happy-rule result=%+v", report.Cases[0])
	}
	if report.Cases[1].Name != "happy-skill" || report.Cases[1].Status != StatusSkip {
		t.Fatalf("happy-skill result=%+v", report.Cases[1])
	}
	if report.Summary.Passed != 1 || report.Summary.Skipped != 1 || report.Summary.Failed != 0 {
		t.Fatalf("summary=%+v", report.Summary)
	}
}

func TestRun_CrashyBinaryMatchesAbnormalExitCases(t *testing.T) {
	t.Parallel()

	crashy := ensureCrashyBinary(t)
	cases := []Case{
		{
			Name:        "abnormal-exit-1",
			Description: "crashy exits before initialize",
			IR:          []byte(`{"nodes":[{"id":"a","kind":"rule"}]}`),
			Manifest: CaseManifest{
				DeclaredOutputs: []contract.DeclaredOutput{{Path: ".echo", Mode: contract.OutputModeOwnedSubdir}},
				Capabilities: contract.Capabilities{
					ConceptKinds: map[string]contract.CapabilityLevel{"rule": contract.CapabilitySupported},
				},
			},
			Expect: Expected{Kind: ExpectedKindError, Error: "SubprocessExitError"},
		},
		{
			Name:        "abnormal-exit-2",
			Description: "crashy exits before initialize",
			IR:          []byte(`{"nodes":[{"id":"b","kind":"skill"}]}`),
			Manifest: CaseManifest{
				DeclaredOutputs: []contract.DeclaredOutput{{Path: ".echo", Mode: contract.OutputModeOwnedSubdir}},
				Capabilities: contract.Capabilities{
					ConceptKinds: map[string]contract.CapabilityLevel{"skill": contract.CapabilitySupported},
				},
			},
			Expect: Expected{Kind: ExpectedKindError, Error: "SubprocessExitError"},
		},
	}

	report, err := Run(context.Background(), crashy, RunOptions{Cases: cases})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, result := range report.Cases {
		if result.Status != StatusPass {
			t.Fatalf("result=%+v want pass", result)
		}
	}
	if report.Summary.Passed != 2 || report.Summary.Failed != 0 {
		t.Fatalf("summary=%+v", report.Summary)
	}
}

func TestRun_ContextCancellationAbortsCorpus(t *testing.T) {
	t.Parallel()

	echo := ensureEchoBinary(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, echo, RunOptions{Cases: mustCasesByName(t, "happy-rule")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRun_SessionSpawnFailureMarksCasesFailedAndContinues(t *testing.T) {
	t.Parallel()

	binary := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(binary, []byte("plain text"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	report, err := Run(context.Background(), binary, RunOptions{Cases: mustCasesByName(t, "happy-rule", "happy-command")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Cases) != 2 {
		t.Fatalf("len(report.Cases)=%d want 2", len(report.Cases))
	}
	if report.Summary.Total != 2 || report.Summary.Failed != 2 {
		t.Fatalf("summary=%+v", report.Summary)
	}
	for _, result := range report.Cases {
		if result.Status != StatusFail || !strings.Contains(result.Reason, "session spawn failed") {
			t.Fatalf("result=%+v", result)
		}
	}
}

func TestMatchOps_OrderInsensitiveByDefault(t *testing.T) {
	t.Parallel()

	expected := []contract.OpRecord{
		{Op: contract.OpKindMkdir, Path: ".echo"},
		{Op: contract.OpKindWriteFile, Path: ".echo/a.md"},
	}
	actual := []contract.OpRecord{
		{Op: contract.OpKindWriteFile, Path: ".echo/a.md"},
		{Op: contract.OpKindMkdir, Path: ".echo"},
	}

	ok, missing, extra := MatchOps(expected, actual, false)
	if !ok || len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("ok=%v missing=%v extra=%v", ok, missing, extra)
	}
}

func TestMatchOps_ReportsMissingAndExtra(t *testing.T) {
	t.Parallel()

	expected := []contract.OpRecord{
		{Op: contract.OpKindMkdir, Path: ".echo"},
		{Op: contract.OpKindWriteFile, Path: ".echo/a.md"},
	}
	actual := []contract.OpRecord{
		{Op: contract.OpKindMkdir, Path: ".echo"},
		{Op: contract.OpKindDelete, Path: ".echo/b.md"},
	}

	ok, missing, extra := MatchOps(expected, actual, false)
	if ok {
		t.Fatal("expected mismatch")
	}
	if len(missing) != 1 || missing[0].Path != ".echo/a.md" {
		t.Fatalf("missing=%v", missing)
	}
	if len(extra) != 1 || extra[0].Path != ".echo/b.md" {
		t.Fatalf("extra=%v", extra)
	}
}

func TestRun_FailingOpsCaseIncludesExpectedActualMissingExtraAndReason(t *testing.T) {
	t.Parallel()

	echo := ensureEchoBinary(t)
	cases := mustCasesByName(t, "happy-rule")
	cases[0].Expect.Ops = []contract.OpRecord{{Op: contract.OpKindWriteFile, Path: ".echo/wrong.md"}}

	report, err := Run(context.Background(), echo, RunOptions{Cases: cases})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Cases) != 1 {
		t.Fatalf("len(report.Cases)=%d want 1", len(report.Cases))
	}

	result := report.Cases[0]
	if result.Status != StatusFail {
		t.Fatalf("result=%+v", result)
	}
	if len(result.ExpectedOps) == 0 || len(result.ActualOps) == 0 || len(result.MissingOps) == 0 || len(result.ExtraOps) == 0 {
		t.Fatalf("result=%+v", result)
	}
	if result.Reason == "" {
		t.Fatalf("result=%+v", result)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(data)
	for _, key := range []string{`"expected_ops":`, `"actual_ops":`, `"missing_ops":`, `"extra_ops":`, `"reason":`} {
		if !strings.Contains(text, key) {
			t.Fatalf("json=%s missing %s", text, key)
		}
	}
}

func mustCasesByName(t *testing.T, names ...string) []Case {
	t.Helper()

	all, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	index := make(map[string]Case, len(all))
	for _, tc := range all {
		index[tc.Name] = tc
	}
	out := make([]Case, 0, len(names))
	for _, name := range names {
		tc, ok := index[name]
		if !ok {
			t.Fatalf("missing corpus case %q", name)
		}
		out = append(out, tc)
	}
	return out
}

func ensureEchoBinary(t *testing.T) string {
	t.Helper()
	echoBinaryOnce.Do(func() {
		echoBinaryPath, echoBinaryErr = buildTestdataBinary("echo", "echo")
	})
	if echoBinaryErr != nil {
		t.Fatalf("build echo binary: %v", echoBinaryErr)
	}
	return echoBinaryPath
}

func ensureCrashyBinary(t *testing.T) string {
	t.Helper()
	crashyBinaryOnce.Do(func() {
		crashyBinaryPath, crashyBinaryErr = buildTestdataBinary("crashy", "crashy")
	})
	if crashyBinaryErr != nil {
		t.Fatalf("build crashy binary: %v", crashyBinaryErr)
	}
	return crashyBinaryPath
}

func buildTestdataBinary(srcSubdir, binPrefix string) (string, error) {
	baseDir := filepath.Join("..")
	srcDir := filepath.Join("testdata", srcSubdir)
	srcFile := filepath.Join(srcDir, "main.go")
	src, err := os.ReadFile(filepath.Join(baseDir, srcFile))
	if err != nil {
		return "", err
	}
	conformanceTestBinaryDirOnce.Do(func() {
		conformanceTestBinaryDir, conformanceTestBinaryDirErr = os.MkdirTemp("", "aienvs-conformance-test-bin-")
	})
	if conformanceTestBinaryDirErr != nil {
		return "", conformanceTestBinaryDirErr
	}

	hash := sha256.Sum256(append(src, []byte(runtime.Version())...))
	suffix := hex.EncodeToString(hash[:8])
	binName := binPrefix + "-" + suffix
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out := filepath.Join(conformanceTestBinaryDir, binName)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	cmd := exec.Command("go", "build", "-o", out, "./"+filepath.ToSlash(srcDir))
	cmd.Dir = baseDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}
