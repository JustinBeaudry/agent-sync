package conformance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type specFixture struct {
	Case
	SpecJSON json.RawMessage `json:"spec_json"`
}

type specExample struct {
	FixtureName string
	JSON        json.RawMessage
}

func TestSpecLockedExamplesMatchFixturesAndHarness(t *testing.T) {
	t.Parallel()

	examples, err := loadSpecExamples(filepath.Join("..", "..", "..", "docs", "spec", "adapter-protocol-v1.md"))
	if err != nil {
		specDocPath := filepath.Join("..", "..", "..", "docs", "spec", "adapter-protocol-v1.md")
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("spec doc not found at %s -- run go test from the repo root", specDocPath)
		}
		t.Fatalf("load spec examples: %v", err)
	}
	if len(examples) == 0 {
		return
	}

	fixtures := make([]Case, 0, len(examples))
	for _, example := range examples {
		fixture, err := loadSpecFixture(example.FixtureName)
		if err != nil {
			t.Fatalf("fixture %q: %v", example.FixtureName, err)
		}
		if len(fixture.SpecJSON) == 0 {
			t.Fatalf("fixture %q missing spec_json payload", example.FixtureName)
		}
		if !jsonEqual(fixture.SpecJSON, example.JSON) {
			t.Fatalf("fixture %q spec_json does not match markdown example\nfixture=%s\nmarkdown=%s", example.FixtureName, fixture.SpecJSON, example.JSON)
		}
		fixtures = append(fixtures, fixture.Case)
	}

	report, err := Run(context.Background(), ensureReferenceEchoBinaryForSpec(t), RunOptions{Cases: fixtures})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.Failed != 0 {
		t.Fatalf("summary=%+v cases=%+v", report.Summary, report.Cases)
	}
}

var (
	specEchoBinaryOnce sync.Once
	specEchoBinaryPath string
	specEchoBinaryErr  error
)

func ensureReferenceEchoBinaryForSpec(t *testing.T) string {
	t.Helper()
	specEchoBinaryOnce.Do(func() {
		specEchoBinaryPath, specEchoBinaryErr = buildReferenceEchoBinaryForSpec()
	})
	if specEchoBinaryErr != nil {
		t.Fatalf("build reference echo binary: %v", specEchoBinaryErr)
	}
	return specEchoBinaryPath
}

func buildReferenceEchoBinaryForSpec() (string, error) {
	rootDir := filepath.Join("..", "..", "..")
	srcPath := filepath.Join(rootDir, "conformance", "echo", "main.go")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(append(src, []byte(runtime.Version())...))
	name := "spec-echo-" + hex.EncodeToString(hash[:8])
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	conformanceTestBinaryDirOnce.Do(func() {
		conformanceTestBinaryDir, conformanceTestBinaryDirErr = os.MkdirTemp("", "aienvs-conformance-test-bin-")
	})
	if conformanceTestBinaryDirErr != nil {
		return "", conformanceTestBinaryDirErr
	}
	out := filepath.Join(conformanceTestBinaryDir, name)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	cmd := exec.Command("go", "build", "-o", out, "./conformance/echo")
	cmd.Dir = rootDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}

func loadSpecExamples(path string) ([]specExample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	examples := make([]specExample, 0)
	var pendingName string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "```aienvs:fixture-name"):
			if pendingName != "" {
				return nil, fmt.Errorf("directive for %q missing json block before next directive", pendingName)
			}
			if i+1 >= len(lines) {
				return nil, fmt.Errorf("directive at line %d missing fixture name", i+1)
			}
			name := strings.TrimSpace(lines[i+1])
			if name == "" {
				return nil, fmt.Errorf("directive at line %d has empty fixture name", i+1)
			}
			if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") || !validFixtureName(name) {
				return nil, fmt.Errorf("invalid fixture name %q: must match [a-z0-9][a-z0-9-]*", name)
			}
			if i+2 >= len(lines) || strings.TrimSpace(lines[i+2]) != "```" {
				return nil, fmt.Errorf("directive for %q missing closing fence", name)
			}
			pendingName = name
			i += 2
		case pendingName != "" && strings.TrimSpace(line) == "```json":
			start := i + 1
			end := start
			for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
				end++
			}
			if end >= len(lines) {
				return nil, fmt.Errorf("json block for %q missing closing fence", pendingName)
			}
			payload := strings.Join(lines[start:end], "\n")
			if !json.Valid([]byte(payload)) {
				return nil, fmt.Errorf("json block for %q is not valid JSON", pendingName)
			}
			examples = append(examples, specExample{
				FixtureName: pendingName,
				JSON:        json.RawMessage(payload),
			})
			pendingName = ""
			i = end
		}
	}

	if pendingName != "" {
		return nil, fmt.Errorf("directive for %q was not followed by a json block", pendingName)
	}
	return examples, nil
}

func loadSpecFixture(name string) (specFixture, error) {
	raw, err := fs.ReadFile(corpusFS, "corpus/"+name+".json")
	if err != nil {
		return specFixture{}, err
	}
	var fixture specFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return specFixture{}, err
	}
	return fixture, nil
}

func jsonEqual(a, b json.RawMessage) bool {
	var av any
	var bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ab, err := json.Marshal(av)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(bv)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

func validFixtureName(name string) bool {
	if name == "" {
		return false
	}
	first := name[0]
	if (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			continue
		}
		return false
	}
	return true
}
