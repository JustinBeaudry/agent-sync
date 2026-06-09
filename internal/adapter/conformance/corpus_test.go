package conformance

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/agent-sync/agent-sync/internal/ir"
)

func TestLoadCorpus_SortsCasesByName(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"corpus/zeta.json":  {Data: []byte(validCaseJSON("zeta", "rule"))},
		"corpus/alpha.json": {Data: []byte(validCaseJSON("alpha", "rule"))},
		"corpus/beta.json":  {Data: []byte(validCaseJSON("beta", "rule"))},
	}

	cases, err := loadCorpus(fsys)
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}

	names := []string{cases[0].Name, cases[1].Name, cases[2].Name}
	if want := []string{"alpha", "beta", "zeta"}; !slices.Equal(names, want) {
		t.Fatalf("names=%v want %v", names, want)
	}
}

func TestLoadCorpus_AllEmbeddedFixturesParse(t *testing.T) {
	t.Parallel()

	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("LoadCorpus returned no cases")
	}
}

func TestLoadCorpus_EmptyCorpusDirectory(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"corpus/.gitkeep": {Data: []byte{}},
	}

	cases, err := loadCorpus(fsys)
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}
	if len(cases) != 0 {
		t.Fatalf("len(cases)=%d want 0", len(cases))
	}
}

func TestLoadCorpus_MalformedJSON(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"corpus/bad.json": {Data: []byte(`{"name":"bad",`)},
	}

	_, err := loadCorpus(fsys)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("errors.Is(%v, ErrInvalidCorpus)=false", err)
	}
}

func TestLoadCorpus_RejectsMissingName(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"corpus/bad.json": {Data: []byte(`{"description":"x","ir":{"nodes":[{"id":"a","kind":"rule"}]},"manifest":{"declared_outputs":[{"path":".echo","mode":"owned-subdir"}],"capabilities":{"concept_kinds":{"rule":"supported"}}},"expect":{"kind":"ops","ops":[{"op":"mkdir","path":".echo"}]}}`)},
	}

	_, err := loadCorpus(fsys)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("errors.Is(%v, ErrInvalidCorpus)=false", err)
	}
}

func TestLoadCorpus_RejectsConflictingExpectPayloads(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"corpus/conflict.json": {Data: []byte(`{"name":"conflict","description":"x","ir":{"nodes":[{"id":"a","kind":"rule"}]},"manifest":{"declared_outputs":[{"path":".echo","mode":"owned-subdir"}],"capabilities":{"concept_kinds":{"rule":"supported"}}},"expect":{"kind":"ops","ops":[{"op":"mkdir","path":".echo"}],"error":"ErrAdapterCapabilityLied"}}`)},
	}

	_, err := loadCorpus(fsys)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("errors.Is(%v, ErrInvalidCorpus)=false", err)
	}
}

func TestLoadCorpus_HappyFixturesCoverAllIRKinds(t *testing.T) {
	t.Parallel()

	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	kinds := make(map[ir.Kind]bool, len(ir.AllKinds()))
	for _, tc := range cases {
		if tc.Expect.Kind != ExpectedKindOps {
			continue
		}
		var payload struct {
			Nodes []struct {
				Kind ir.Kind `json:"kind"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(tc.IR, &payload); err != nil {
			t.Fatalf("case %s: decode IR: %v", tc.Name, err)
		}
		for _, node := range payload.Nodes {
			kinds[node.Kind] = true
		}
	}

	for _, kind := range ir.AllKinds() {
		if !kinds[kind] {
			t.Fatalf("missing happy-path fixture covering IR kind %q", kind)
		}
	}
}

func TestLoadCorpus_AllErrorExpectationsAreRecognized(t *testing.T) {
	t.Parallel()

	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	for _, tc := range cases {
		if tc.Expect.Kind != ExpectedKindError {
			continue
		}
		if !isKnownExpectedError(tc.Expect.Error) {
			t.Fatalf("case %s uses unknown error expectation %q", tc.Name, tc.Expect.Error)
		}
	}
}

func isKnownExpectedError(name string) bool {
	switch name {
	case "ErrAdapterCookieMissing",
		"ErrAdapterCookieMismatch",
		"ErrAdapterProtocolMismatch",
		"ErrAdapterUndeclaredOutput",
		"ErrAdapterProtocolOrderViolation",
		"ErrAdapterCapabilityLied",
		"ErrAdapterTimeout",
		"ErrFrameTooLarge",
		"SubprocessExitError":
		return true
	default:
		return false
	}
}

func validCaseJSON(name, kind string) string {
	return `{"name":"` + name + `","description":"fixture","ir":{"nodes":[{"id":"` + name + `","kind":"` + kind + `"}]},"manifest":{"declared_outputs":[{"path":".echo","mode":"owned-subdir"}],"capabilities":{"concept_kinds":{"` + kind + `":"supported"},"write_tool_owned":true}},"expect":{"kind":"ops","ops":[{"op":"mkdir","path":".echo"},{"op":"write_file","path":".echo/` + name + `.md"}]}}`
}
