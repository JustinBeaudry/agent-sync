package adapterkit

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	contractschema "github.com/agent-sync/agent-sync/internal/adapter/contract"
)

func TestSchemaParity_SamplesValidateAgainstContractSchemas(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		schemaName string
		schemaPath []string
		value      any
	}{
		{
			name:       "InitializeParams",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "params"},
			value: InitializeParams{
				Client:           "test",
				ProtocolVersions: []string{ContractVersionV1},
				Cookie:           "cookie",
				WorkspaceRoot:    "/tmp/ws",
				ReservedPrefix:   ".echo",
				IRVersion:        "v1",
				Scope:            "project",
			},
		},
		{
			name:       "InitializeParams_GlobalScope",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "params"},
			value: InitializeParams{
				Client:           "test",
				ProtocolVersions: []string{ContractVersionV1},
				Cookie:           "cookie",
				WorkspaceRoot:    "/tmp/ws",
				ReservedPrefix:   ".echo",
				IRVersion:        "v1",
				Scope:            "global",
			},
		},
		{
			name:       "InitializeResult",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "result"},
			value: InitializeResult{
				Server:          "echo/0.1",
				ProtocolVersion: ContractVersionV1,
				Capabilities:    NewCapabilities().Supports("rule").WithWriteToolOwned(true).Build(),
				DeclaredOutputs: []DeclaredOutput{{Path: ".echo", Mode: OutputModeOwnedSubdir}},
				Cookie:          "0123456789abcdef0123456789abcdef",
			},
		},
		{
			name:       "Capabilities",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "capabilities"},
			value:      NewCapabilities().Supports("rule").Unsupported("skill").WithWriteToolOwned(true).Build(),
		},
		{
			name:       "DeclaredOutput",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "declared_output"},
			value:      DeclaredOutput{Path: ".echo", Mode: OutputModeOwnedSubdir},
		},
		{
			name:       "DeclaredOutput (shared-subdir)",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "declared_output"},
			value:      DeclaredOutput{Path: ".agents/skills", Mode: OutputModeSharedSubdir},
		},
		{
			name:       "DeclaredOutput (file-leaf)",
			schemaName: "initialize",
			schemaPath: []string{"$defs", "declared_output"},
			value:      DeclaredOutput{Path: ".cursor/commands", Mode: OutputModeFileLeaf},
		},
		{
			name:       "EmitParams",
			schemaName: "emit",
			schemaPath: []string{"$defs", "params"},
			value:      EmitParams{Target: "test", IR: []byte(`{"nodes":[{"id":"a","kind":"rule"}]}`)},
		},
		{
			name:       "EmitResult",
			schemaName: "emit",
			schemaPath: []string{"$defs", "result"},
			value:      EmitResult{OpsPerformed: []OpRecord{{Op: OpKindMkdir, Path: ".echo"}}},
		},
		{
			name:       "OpRecord",
			schemaName: "emit",
			schemaPath: []string{"$defs", "op_record"},
			value:      OpRecord{Op: OpKindWriteFile, Path: ".echo/a.md"},
		},
		{
			name:       "ShutdownParams",
			schemaName: "shutdown",
			schemaPath: []string{"$defs", "params"},
			value:      ShutdownParams{},
		},
		{
			name:       "ShutdownResult",
			schemaName: "shutdown",
			schemaPath: []string{"$defs", "result"},
			value:      ShutdownResult{},
		},
		{
			name:       "OpWriteFile",
			schemaName: "op_write_file",
			value:      mustSchemaOpWriteFile(t),
		},
		{
			name:       "OpWriteToolOwned",
			schemaName: "op_write_tool_owned",
			value:      OpWriteToolOwned{Path: ".mcp.json", Kind: ToolOwnedKindJSONPointer, Locator: "/mcpServers/echo", Content: []byte(`{"command":"echo"}`)},
		},
		{
			name:       "OpMkdir",
			schemaName: "op_mkdir",
			value:      OpMkdir{Path: ".echo", Mode: 0o755},
		},
		{
			name:       "OpDelete",
			schemaName: "op_delete",
			value:      OpDelete{Path: ".echo/a.md"},
		},
		{
			name:       "OpWarning",
			schemaName: "op_warning",
			value:      OpWarning{ConceptID: "rule-1", Status: WarningStatusPartial, Note: "not implemented"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schemaBytes, err := contractschema.LoadSchema(tc.schemaName)
			if err != nil {
				t.Fatalf("LoadSchema: %v", err)
			}
			valueBytes, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if tc.name == "InitializeResult" && !strings.Contains(string(valueBytes), `"cookie":"0123456789abcdef0123456789abcdef"`) {
				t.Fatalf("initialize result json missing cookie: %s", valueBytes)
			}
			if err := validateJSONAgainstSchema(schemaBytes, tc.schemaPath, valueBytes); err != nil {
				t.Fatalf("schema validation failed: %v\njson=%s", err, valueBytes)
			}
		})
	}
}

func TestSchemaParity_AllMethodAndOpSchemasRemainLoadable(t *testing.T) {
	t.Parallel()

	for _, name := range []string{MethodInitialize, MethodInitialized, MethodEmit, MethodShutdown} {
		if _, err := contractschema.LoadSchema(name); err != nil {
			t.Fatalf("LoadSchema(%q): %v", name, err)
		}
	}
	for _, kind := range AllOpKinds() {
		if _, err := contractschema.LoadSchema("op_" + string(kind)); err != nil {
			t.Fatalf("LoadSchema(op_%s): %v", kind, err)
		}
	}
}

func validateJSONAgainstSchema(schemaBytes []byte, schemaPath []string, valueBytes []byte) error {
	var root any
	if err := json.Unmarshal(schemaBytes, &root); err != nil {
		return err
	}
	schema, err := walkSchema(root, schemaPath)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(valueBytes, &value); err != nil {
		return err
	}
	return validateValue(root, schema, value)
}

func walkSchema(root any, path []string) (any, error) {
	cur := root
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema path %v: expected object at %q", path, key)
		}
		next, ok := obj[key]
		if !ok {
			return nil, fmt.Errorf("schema path %v: missing key %q", path, key)
		}
		cur = next
	}
	return cur, nil
}

func validateValue(root any, schema any, value any) error {
	schemaObj, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := schemaObj["$ref"].(string); ok {
		resolved, err := resolveSchemaRef(root, ref)
		if err != nil {
			return err
		}
		return validateValue(root, resolved, value)
	}

	if constValue, ok := schemaObj["const"]; ok {
		if value != constValue {
			return fmt.Errorf("const mismatch: got %v want %v", value, constValue)
		}
	}
	if enumValues, ok := schemaObj["enum"].([]any); ok {
		for _, candidate := range enumValues {
			if value == candidate {
				goto enumOK
			}
		}
		return fmt.Errorf("enum mismatch: got %v not in %v", value, enumValues)
	}
enumOK:

	switch schemaObj["type"] {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("want object, got %T", value)
		}
		if required, ok := schemaObj["required"].([]any); ok {
			for _, req := range required {
				key, _ := req.(string)
				if _, ok := obj[key]; !ok {
					return fmt.Errorf("missing required property %q", key)
				}
			}
		}
		if props, ok := schemaObj["properties"].(map[string]any); ok {
			for key, propSchema := range props {
				got, ok := obj[key]
				if !ok {
					continue
				}
				if err := validateValue(root, propSchema, got); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
			}
		}
		if additional, ok := schemaObj["additionalProperties"]; ok {
			props := map[string]struct{}{}
			if propDefs, ok := schemaObj["properties"].(map[string]any); ok {
				for key := range propDefs {
					props[key] = struct{}{}
				}
			}
			for key, got := range obj {
				if _, ok := props[key]; ok {
					continue
				}
				if err := validateValue(root, additional, got); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
			}
		}
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return fmt.Errorf("want array, got %T", value)
		}
		itemSchema := schemaObj["items"]
		for i, item := range arr {
			if err := validateValue(root, itemSchema, item); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("want string, got %T", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("want boolean, got %T", value)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("want integer, got %T", value)
		}
		if math.Trunc(number) != number {
			return fmt.Errorf("want integer, got non-integer %v", number)
		}
		if minimum, ok := schemaObj["minimum"].(float64); ok && number < minimum {
			return fmt.Errorf("integer %v < minimum %v", number, minimum)
		}
		if maximum, ok := schemaObj["maximum"].(float64); ok && number > maximum {
			return fmt.Errorf("integer %v > maximum %v", number, maximum)
		}
	}

	return nil
}

func resolveSchemaRef(root any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported schema ref %q", ref)
	}
	path := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	return walkSchema(root, path)
}

func mustSchemaOpWriteFile(t *testing.T) OpWriteFile {
	t.Helper()
	op, err := NewOpWriteFile(".echo/a.md", 0o644, []byte("hello"))
	if err != nil {
		t.Fatalf("NewOpWriteFile: %v", err)
	}
	return op
}
