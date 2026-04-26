package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aienvs/aienvs/internal/ir"
	"github.com/aienvs/aienvs/pkg/adapterkit"
)

const (
	serverName    = "echo"
	serverVersion = "0.1"
	outputRoot    = ".echo"
)

type emitDocument struct {
	Nodes []emitNode `json:"nodes"`
}

type emitNode struct {
	ID   string          `json:"id"`
	Body json.RawMessage `json:"body"`
}

func main() {
	if err := run(context.Background()); err != nil {
		code := adapterkit.ExitCode(err)
		if code != 0 {
			os.Exit(code)
		}
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	server := adapterkit.NewServer(serverName, serverVersion)
	server.OnInitialize(func(context.Context, adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
		return adapterkit.InitializeResult{
			Capabilities:    buildCapabilities(),
			DeclaredOutputs: []adapterkit.DeclaredOutput{{Path: outputRoot, Mode: adapterkit.OutputModeOwnedSubdir}},
		}, nil
	})
	server.OnEmit(func(ctx context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
		return handleEmit(ctx, params)
	})
	return server.Run(ctx)
}

func buildCapabilities() adapterkit.Capabilities {
	builder := adapterkit.NewCapabilities().WithWriteToolOwned(true)
	for _, kind := range ir.AllKinds() {
		builder.Supports(string(kind))
	}
	return builder.Build()
}

func handleEmit(_ context.Context, params adapterkit.EmitParams) (adapterkit.EmitResult, error) {
	var doc emitDocument
	if err := json.Unmarshal(params.IR, &doc); err != nil {
		return adapterkit.EmitResult{}, fmt.Errorf("echo: decode emit IR: %w", err)
	}

	ops := make([]adapterkit.OpRecord, 0, len(doc.Nodes)+1)
	if len(doc.Nodes) > 0 {
		mkdir := adapterkit.OpMkdir{Path: outputRoot, Mode: 0o755}
		if _, err := json.Marshal(mkdir); err != nil {
			return adapterkit.EmitResult{}, fmt.Errorf("echo: marshal mkdir op: %w", err)
		}
		ops = append(ops, adapterkit.OpRecord{Op: mkdir.OpKind(), Path: mkdir.OpPath()})
	}

	for _, node := range doc.Nodes {
		content, err := nodeContent(node.Body)
		if err != nil {
			return adapterkit.EmitResult{}, fmt.Errorf("echo: node %q body: %w", node.ID, err)
		}
		writeOp, err := adapterkit.NewOpWriteFile(outputRoot+"/"+node.ID+".md", 0o644, content)
		if err != nil {
			return adapterkit.EmitResult{}, fmt.Errorf("echo: build write_file op for %q: %w", node.ID, err)
		}
		if _, err := json.Marshal(writeOp); err != nil {
			return adapterkit.EmitResult{}, fmt.Errorf("echo: marshal write_file op for %q: %w", node.ID, err)
		}
		ops = append(ops, adapterkit.OpRecord{Op: writeOp.OpKind(), Path: writeOp.OpPath()})
	}

	return adapterkit.EmitResult{OpsPerformed: ops}, nil
}

func nodeContent(raw json.RawMessage) ([]byte, error) {
	trimmed := raw
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return []byte(text), nil
	}

	var anyValue any
	if err := json.Unmarshal(trimmed, &anyValue); err != nil {
		return nil, errors.New("body must be valid JSON")
	}
	return trimmed, nil
}
