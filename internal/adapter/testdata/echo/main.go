// Command echo is a minimal protocol-speaking adapter binary used
// only by internal/adapter/subprocess_test.go's integration tests.
// It is NOT a reference implementation — PR 3 ships a separate,
// complete echo reference in conformance/echo/. This binary exists
// to exercise the subprocess transport against a real OS process.
//
// The binary reads frames from stdin, classifies each via the
// internal/adapter/contract package, and responds:
//   - initialize     → echoes the cookie + minimal capabilities +
//     one declared output ".echo/"
//   - initialized    → ignored (no response)
//   - emit           → returns one mkdir + one write_file op record
//     under .echo/<id> for each IR node
//   - shutdown       → returns empty result, exits 0
//
// Reads AGENT_SYNC_ADAPTER_COOKIE; exits 7 if missing. Writes a single
// "echo: started\n" line to stderr so the runtime's stderr ring
// buffer has something to capture.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/agent-sync/agent-sync/internal/adapter/contract"
)

var nodeIDPattern = regexp.MustCompile(`\A[a-z0-9][a-z0-9_-]{0,63}\z`)

func main() {
	cookie := os.Getenv("AGENT_SYNC_ADAPTER_COOKIE")
	if cookie == "" {
		fmt.Fprintln(os.Stderr, "echo: AGENT_SYNC_ADAPTER_COOKIE not set")
		os.Exit(7)
	}
	fmt.Fprintln(os.Stderr, "echo: started")

	stdin := bufio.NewReader(os.Stdin)
	fr := contract.NewFrameReader(stdin)

	for {
		frame, err := fr.Read(contract.DefaultMaxFrameBytes)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "echo: read frame: %v\n", err)
			os.Exit(2)
		}
		msg, err := contract.ParseMessage(frame)
		if err != nil {
			fmt.Fprintf(os.Stderr, "echo: parse: %v\n", err)
			os.Exit(3)
		}
		switch msg.Kind {
		case contract.MessageKindNotification:
			// initialized → ignore
			continue
		case contract.MessageKindRequest:
			resp := handle(msg, cookie)
			out, err := json.Marshal(resp)
			if err != nil {
				fmt.Fprintf(os.Stderr, "echo: marshal: %v\n", err)
				os.Exit(4)
			}
			if err := contract.WriteFrame(os.Stdout, out); err != nil {
				fmt.Fprintf(os.Stderr, "echo: write: %v\n", err)
				os.Exit(5)
			}
			if msg.Method == contract.MethodShutdown {
				return
			}
		}
	}
}

func handle(req contract.Message, cookie string) contract.Response {
	switch req.Method {
	case contract.MethodInitialize:
		return contract.Response{ID: req.ID, Result: initResult(cookie)}
	case contract.MethodEmit:
		result, errObj := emitResult(req)
		if errObj != nil {
			return contract.Response{ID: req.ID, Error: errObj}
		}
		return contract.Response{ID: req.ID, Result: result}
	case contract.MethodShutdown:
		b, _ := json.Marshal(contract.ShutdownResult{})
		return contract.Response{ID: req.ID, Result: b}
	}
	return contract.Response{
		ID:    req.ID,
		Error: &contract.Error{Code: contract.CodeMethodNotFound, Message: "unknown: " + req.Method},
	}
}

func initResult(cookie string) json.RawMessage {
	wire := contract.InitializeResult{
		Server:          "echo/0.1",
		ProtocolVersion: "agent-sync/v1",
		Capabilities: contract.Capabilities{
			ConceptKinds: map[string]contract.CapabilityLevel{
				"rule": contract.CapabilitySupported,
			},
			WriteToolOwned: true,
		},
		DeclaredOutputs: []contract.DeclaredOutput{
			{Path: ".echo", Mode: contract.OutputModeOwnedSubdir},
		},
		Cookie: cookie,
	}
	b, _ := json.Marshal(wire)
	return b
}

func emitResult(req contract.Message) (json.RawMessage, *contract.Error) {
	// Synthesize a small ops_performed list from the IR's nodes (if any).
	var params struct {
		Target string `json:"target"`
		IR     struct {
			Nodes []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"nodes"`
		} `json:"ir"`
	}
	_ = json.Unmarshal(req.Params, &params)

	ops := []contract.OpRecord{
		{Op: contract.OpKindMkdir, Path: ".echo"},
	}
	for _, n := range params.IR.Nodes {
		if !nodeIDPattern.MatchString(n.ID) {
			return nil, &contract.Error{Code: contract.CodeInvalidParams, Message: fmt.Sprintf("invalid node id %q", n.ID)}
		}
		ops = append(ops, contract.OpRecord{
			Op:   contract.OpKindWriteFile,
			Path: ".echo/" + n.ID + ".md",
		})
	}
	result := contract.EmitResult{OpsPerformed: ops}
	b, _ := json.Marshal(result)
	return b, nil
}
