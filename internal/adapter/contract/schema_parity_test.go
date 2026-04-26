package contract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestSchemaParity_GoStructsMatchJSONSchemas asserts that every Go
// struct's JSON tags equal the corresponding schema's properties keys —
// in both directions — so a rename in either side fails CI.
//
// The test uses the *wire* form for ops (opWriteFileWire et al.) and
// the public form for everything else, because op Go-types hide the
// encoding field from callers but it's part of the wire contract.
func TestSchemaParity_GoStructsMatchJSONSchemas(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		schemaFile    string
		schemaNavPath []string // path through the schema JSON to the properties object
		goType        reflect.Type
	}{
		{
			name:          "InitializeParams",
			schemaFile:    "initialize.json",
			schemaNavPath: []string{"$defs", "params", "properties"},
			goType:        reflect.TypeOf(InitializeParams{}),
		},
		{
			name:          "InitializeResult",
			schemaFile:    "initialize.json",
			schemaNavPath: []string{"$defs", "result", "properties"},
			goType:        reflect.TypeOf(InitializeResult{}),
		},
		{
			name:          "Capabilities",
			schemaFile:    "initialize.json",
			schemaNavPath: []string{"$defs", "capabilities", "properties"},
			goType:        reflect.TypeOf(Capabilities{}),
		},
		{
			name:          "DeclaredOutput",
			schemaFile:    "initialize.json",
			schemaNavPath: []string{"$defs", "declared_output", "properties"},
			goType:        reflect.TypeOf(DeclaredOutput{}),
		},
		{
			name:          "EmitParams",
			schemaFile:    "emit.json",
			schemaNavPath: []string{"$defs", "params", "properties"},
			goType:        reflect.TypeOf(EmitParams{}),
		},
		{
			name:          "EmitResult",
			schemaFile:    "emit.json",
			schemaNavPath: []string{"$defs", "result", "properties"},
			goType:        reflect.TypeOf(EmitResult{}),
		},
		{
			name:          "OpRecord",
			schemaFile:    "emit.json",
			schemaNavPath: []string{"$defs", "op_record", "properties"},
			goType:        reflect.TypeOf(OpRecord{}),
		},
		{
			name:          "ShutdownParams",
			schemaFile:    "shutdown.json",
			schemaNavPath: []string{"$defs", "params", "properties"},
			goType:        reflect.TypeOf(ShutdownParams{}),
		},
		{
			name:          "ShutdownResult",
			schemaFile:    "shutdown.json",
			schemaNavPath: []string{"$defs", "result", "properties"},
			goType:        reflect.TypeOf(ShutdownResult{}),
		},
		{
			name:          "OpWriteFileWire",
			schemaFile:    "op_write_file.json",
			schemaNavPath: []string{"properties"},
			goType:        reflect.TypeOf(opWriteFileWire{}),
		},
		{
			name:          "OpWriteToolOwnedWire",
			schemaFile:    "op_write_tool_owned.json",
			schemaNavPath: []string{"properties"},
			goType:        reflect.TypeOf(opWriteToolOwnedWire{}),
		},
		{
			name:          "OpMkdirWire",
			schemaFile:    "op_mkdir.json",
			schemaNavPath: []string{"properties"},
			goType:        reflect.TypeOf(opMkdirWire{}),
		},
		{
			name:          "OpDeleteWire",
			schemaFile:    "op_delete.json",
			schemaNavPath: []string{"properties"},
			goType:        reflect.TypeOf(opDeleteWire{}),
		},
		{
			name:          "OpWarningWire",
			schemaFile:    "op_warning.json",
			schemaNavPath: []string{"properties"},
			goType:        reflect.TypeOf(opWarningWire{}),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			schemaProps, err := loadSchemaProps(c.schemaFile, c.schemaNavPath)
			if err != nil {
				t.Fatalf("load schema %s @ %v: %v", c.schemaFile, c.schemaNavPath, err)
			}
			structTags := jsonTagSet(c.goType)

			missingFromSchema := setDiff(structTags, schemaProps)
			missingFromStruct := setDiff(schemaProps, structTags)
			if len(missingFromSchema) > 0 || len(missingFromStruct) > 0 {
				t.Errorf("parity drift for %s (schema %s):\n  in struct but not schema: %v\n  in schema but not struct: %v",
					c.name, c.schemaFile, missingFromSchema, missingFromStruct)
			}
		})
	}
}

// TestSchemaParity_AllMethodsHaveSchemas asserts every method-name
// constant has a corresponding schema file embedded.
func TestSchemaParity_AllMethodsHaveSchemas(t *testing.T) {
	t.Parallel()

	for _, m := range []string{MethodInitialize, MethodInitialized, MethodEmit, MethodShutdown} {
		path := "schema/" + m + ".json"
		if _, err := SchemaFS.ReadFile(path); err != nil {
			t.Errorf("missing schema for method %q at %s: %v", m, path, err)
		}
	}
}

// TestSchemaParity_AllOpsHaveSchemas asserts every op kind has a
// corresponding schema file embedded.
func TestSchemaParity_AllOpsHaveSchemas(t *testing.T) {
	t.Parallel()

	for _, k := range AllOpKinds() {
		path := "schema/op_" + string(k) + ".json"
		if _, err := SchemaFS.ReadFile(path); err != nil {
			t.Errorf("missing schema for op %q at %s: %v", k, path, err)
		}
	}
}

// TestSchemaParity_SchemasHaveRequiredMetadata asserts every schema
// file declares $schema, $id, and type fields. Catches half-authored
// schemas before they land.
func TestSchemaParity_SchemasHaveRequiredMetadata(t *testing.T) {
	t.Parallel()

	entries, err := SchemaFS.ReadDir("schema")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			t.Parallel()

			data, err := SchemaFS.ReadFile("schema/" + e.Name())
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			for _, key := range []string{"$schema", "$id", "type"} {
				if _, ok := m[key]; !ok {
					t.Errorf("schema %s missing required key %q", e.Name(), key)
				}
			}
		})
	}
}

// loadSchemaProps walks `path` through the parsed schema JSON and
// returns the set of property names at that location. Returns an error
// when the path doesn't resolve to a properties-shaped object.
func loadSchemaProps(file string, path []string) (map[string]struct{}, error) {
	data, err := SchemaFS.ReadFile("schema/" + file)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	cur := doc
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %v: expected object at %q, got %T", path, key, cur)
		}
		next, ok := obj[key]
		if !ok {
			return nil, fmt.Errorf("path %v: missing key %q", path, key)
		}
		cur = next
	}
	props, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("path %v: terminal value is %T, want object", path, cur)
	}
	out := make(map[string]struct{}, len(props))
	for k := range props {
		out[k] = struct{}{}
	}
	return out, nil
}

// jsonTagSet walks t's exported fields, parses their `json:"…"` tags,
// and returns the set of non-skipped JSON names.
func jsonTagSet(t reflect.Type) map[string]struct{} {
	out := make(map[string]struct{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" and friends.
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

// setDiff returns the keys present in a but not b, sorted for stable
// error messages.
func setDiff(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
