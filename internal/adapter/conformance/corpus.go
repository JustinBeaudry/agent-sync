package conformance

import (
	"cmp"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"

	"github.com/aienvs/aienvs/internal/adapter/contract"
)

//go:embed corpus/*.json
var corpusFS embed.FS

// ErrInvalidCorpus reports that one or more embedded corpus fixtures are
// malformed.
var ErrInvalidCorpus = errors.New("conformance: invalid corpus fixture")

// ExpectedKind discriminates the expected outcome for a corpus case.
type ExpectedKind string

const (
	ExpectedKindOps   ExpectedKind = "ops"
	ExpectedKindError ExpectedKind = "error"
	ExpectedKindSkip  ExpectedKind = "skip"
)

// Case is one conformance scenario loaded from the embedded corpus.
type Case struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	IR          json.RawMessage `json:"ir"`
	Manifest    CaseManifest    `json:"manifest"`
	Expect      Expected        `json:"expect"`
}

// CaseManifest captures the minimum initialize response properties a
// case requires from the adapter under test.
type CaseManifest struct {
	DeclaredOutputs []contract.DeclaredOutput `json:"declared_outputs"`
	Capabilities    contract.Capabilities     `json:"capabilities"`
}

// Expected describes the case outcome. Exactly one payload field applies
// based on Kind.
type Expected struct {
	Kind        ExpectedKind        `json:"kind"`
	Ops         []contract.OpRecord `json:"ops,omitempty"`
	Error       string              `json:"error,omitempty"`
	Skip        string              `json:"skip,omitempty"`
	StrictOrder bool                `json:"strict_order,omitempty"`
}

// LoadCorpus returns the embedded corpus cases sorted by name.
func LoadCorpus() ([]Case, error) {
	return loadCorpus(corpusFS)
}

func loadCorpus(fsys fs.FS) ([]Case, error) {
	entries, err := fs.ReadDir(fsys, "corpus")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Case{}, nil
		}
		return nil, fmt.Errorf("conformance: read corpus dir: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	cases := make([]Case, 0, len(files))
	for _, name := range files {
		raw, err := fs.ReadFile(fsys, "corpus/"+name)
		if err != nil {
			return nil, fmt.Errorf("conformance: read corpus/%s: %w", name, err)
		}
		var tc Case
		if err := json.Unmarshal(raw, &tc); err != nil {
			return nil, fmt.Errorf("%w: corpus/%s: decode JSON: %w", ErrInvalidCorpus, name, err)
		}
		if err := validateCase(name, tc); err != nil {
			return nil, err
		}
		cases = append(cases, tc)
	}

	slices.SortFunc(cases, func(a, b Case) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return cases, nil
}

func validateCase(filename string, tc Case) error {
	if tc.Name == "" {
		return fmt.Errorf("%w: corpus/%s: name is required", ErrInvalidCorpus, filename)
	}
	if want := strings.TrimSuffix(filename, ".json"); tc.Name != want {
		return fmt.Errorf("%w: corpus/%s: name %q must match filename %q", ErrInvalidCorpus, filename, tc.Name, want)
	}
	if len(tc.IR) == 0 {
		return fmt.Errorf("%w: corpus/%s: ir is required", ErrInvalidCorpus, filename)
	}
	if len(tc.Expect.Ops) > 0 && tc.Expect.Error != "" {
		return fmt.Errorf("%w: corpus/%s: expect.ops and expect.error are mutually exclusive", ErrInvalidCorpus, filename)
	}
	if len(tc.Expect.Ops) > 0 && tc.Expect.Skip != "" {
		return fmt.Errorf("%w: corpus/%s: expect.ops and expect.skip are mutually exclusive", ErrInvalidCorpus, filename)
	}
	if tc.Expect.Error != "" && tc.Expect.Skip != "" {
		return fmt.Errorf("%w: corpus/%s: expect.error and expect.skip are mutually exclusive", ErrInvalidCorpus, filename)
	}

	switch tc.Expect.Kind {
	case ExpectedKindOps:
		if len(tc.Expect.Ops) == 0 {
			return fmt.Errorf("%w: corpus/%s: expect.kind=%q requires non-empty ops", ErrInvalidCorpus, filename, tc.Expect.Kind)
		}
		if tc.Expect.Error != "" || tc.Expect.Skip != "" {
			return fmt.Errorf("%w: corpus/%s: expect.kind=%q may only set ops", ErrInvalidCorpus, filename, tc.Expect.Kind)
		}
	case ExpectedKindError:
		if tc.Expect.Error == "" {
			return fmt.Errorf("%w: corpus/%s: expect.kind=%q requires error", ErrInvalidCorpus, filename, tc.Expect.Kind)
		}
		if len(tc.Expect.Ops) > 0 || tc.Expect.Skip != "" {
			return fmt.Errorf("%w: corpus/%s: expect.kind=%q may only set error", ErrInvalidCorpus, filename, tc.Expect.Kind)
		}
	case ExpectedKindSkip:
		if tc.Expect.Skip == "" {
			return fmt.Errorf("%w: corpus/%s: expect.kind=%q requires skip reason", ErrInvalidCorpus, filename, tc.Expect.Kind)
		}
		if len(tc.Expect.Ops) > 0 || tc.Expect.Error != "" {
			return fmt.Errorf("%w: corpus/%s: expect.kind=%q may only set skip", ErrInvalidCorpus, filename, tc.Expect.Kind)
		}
	default:
		return fmt.Errorf("%w: corpus/%s: unsupported expect.kind %q", ErrInvalidCorpus, filename, tc.Expect.Kind)
	}

	return nil
}
