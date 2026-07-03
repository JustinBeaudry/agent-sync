package engine

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/agent-sync/agent-sync/internal/adapter"
	"github.com/agent-sync/agent-sync/internal/fsroot"
	"github.com/agent-sync/agent-sync/internal/ir"
	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

// capturedInit is what the capture adapter observed on its initialize
// handshake.
type capturedInit struct {
	sourceURL    string
	sourceCommit string
}

// captureBundledAdapter builds a minimal bundled adapter (via adapterkit,
// exactly like the real bundled adapters) that records the initialize
// params it receives and emits nothing. It declares no supported kinds so
// the capability-lie gate stays quiet for any IR.
func captureBundledAdapter(name string, got chan<- capturedInit) *adapter.BundledAdapter {
	return &adapter.BundledAdapter{
		Manifest: adapter.AdapterManifest{
			Name:            name,
			Version:         "0.1",
			ContractVersion: adapter.ContractVersionV1,
			ReservedPrefix:  "." + name,
			Command:         []string{"agent-sync-adapter-" + name + "-bundled"},
		},
		Run: func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			server := adapterkit.NewServer(adapterkit.ServerOptions{
				Name:    name,
				Version: "0.1",
				Stdin:   stdin,
				Stdout:  stdout,
				Getenv: func(key string) string {
					if key == adapterkit.CookieEnvVar {
						return "00000000000000000000000000000000"
					}
					return ""
				},
			})
			server.OnInitialize(func(_ context.Context, params adapterkit.InitializeParams) (adapterkit.InitializeResult, error) {
				got <- capturedInit{sourceURL: params.SourceURL, sourceCommit: params.SourceCommit}
				return adapterkit.InitializeResult{
					Capabilities:    adapterkit.NewCapabilities().Build(),
					DeclaredOutputs: []adapterkit.DeclaredOutput{},
				}, nil
			})
			server.OnEmit(func(_ context.Context, _ adapterkit.EmitParams) (adapterkit.EmitResult, error) {
				return adapterkit.EmitResult{OpsPerformed: []adapterkit.OpRecord{}}, nil
			})
			if err := server.Run(ctx); err != nil {
				return fmt.Errorf("capture bundled adapter: %w", err)
			}
			return nil
		},
	}
}

// TestRunAdapter_PassesSourceMetadataOnInitialize asserts the engine
// plumbs Request.SourceURL and Request.Commit into the adapter session's
// initialize params (source_url / source_commit) — the session-level
// source metadata adapters render into managed headers (plan U2).
func TestRunAdapter_PassesSourceMetadataOnInitialize(t *testing.T) {
	cases := []struct {
		name       string
		sourceURL  string
		commit     string
		wantURL    string
		wantCommit string
	}{
		{
			name:       "git-backed source passes canonical url and commit",
			sourceURL:  "https://github.com/org/agents.git",
			commit:     "0123456789abcdef0123456789abcdef01234567",
			wantURL:    "https://github.com/org/agents.git",
			wantCommit: "0123456789abcdef0123456789abcdef01234567",
		},
		{
			name:       "local_dir source passes path with empty commit",
			sourceURL:  ".agents",
			commit:     "",
			wantURL:    ".agents",
			wantCommit: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan capturedInit, 1)
			const target = "srccap"
			reg, err := adapter.DiscoverAdapters(context.Background(), adapter.DiscoverOptions{
				Bundled: []*adapter.BundledAdapter{captureBundledAdapter(target, got)},
			})
			if err != nil {
				t.Fatalf("DiscoverAdapters: %v", err)
			}
			ws := t.TempDir()
			root, err := fsroot.OpenWorkspaceRoot(ws)
			if err != nil {
				t.Fatalf("OpenWorkspaceRoot: %v", err)
			}
			t.Cleanup(func() { _ = root.Close() })

			req := Request{
				Root:          root,
				WorkspacePath: ws,
				Registry:      reg,
				Targets:       []string{target},
				Nodes:         []ir.Node{{ID: "r", Kind: ir.KindRule, Version: 1, Body: []byte("x")}},
				Commit:        tc.commit,
				SourceURL:     tc.sourceURL,
				Options:       Options{Now: fixedNow()},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.Cleanup(cancel)
			if _, err := runAdapter(ctx, req, target); err != nil {
				t.Fatalf("runAdapter: %v", err)
			}
			select {
			case c := <-got:
				if c.sourceURL != tc.wantURL {
					t.Errorf("initialize source_url = %q, want %q", c.sourceURL, tc.wantURL)
				}
				if c.sourceCommit != tc.wantCommit {
					t.Errorf("initialize source_commit = %q, want %q", c.sourceCommit, tc.wantCommit)
				}
			default:
				t.Fatal("adapter never received initialize")
			}
		})
	}
}
