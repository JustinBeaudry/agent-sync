package cli

import (
	"context"
	"testing"

	"github.com/agent-sync/agent-sync/internal/tui/wizard"
)

func TestResolvePin_FloatingSkips(t *testing.T) {
	cfg := &wizard.InitConfig{Floating: true, LocalPath: "/whatever"}
	if err := resolvePin(context.Background(), cfg, false); err != nil {
		t.Fatalf("resolvePin: %v", err)
	}
	if cfg.Commit != "" {
		t.Fatalf("floating should not pin a commit, got %q", cfg.Commit)
	}
}

func TestResolvePin_AlreadyPinnedSkips(t *testing.T) {
	cfg := &wizard.InitConfig{Commit: "deadbeef", LocalPath: "/whatever"}
	if err := resolvePin(context.Background(), cfg, false); err != nil {
		t.Fatalf("resolvePin: %v", err)
	}
	if cfg.Commit != "deadbeef" {
		t.Fatalf("preset commit should be preserved, got %q", cfg.Commit)
	}
}

func TestResolvePin_LocalPathResolvesHEAD(t *testing.T) {
	requireGit(t)
	canonical, sha := makeCanonicalRepo(t)
	cfg := &wizard.InitConfig{LocalPath: canonical}
	if err := resolvePin(context.Background(), cfg, false); err != nil {
		t.Fatalf("resolvePin: %v", err)
	}
	if cfg.Commit != sha {
		t.Fatalf("resolved commit = %q, want %q", cfg.Commit, sha)
	}
}

func TestResolvePin_RemoteOfflineFails(t *testing.T) {
	cfg := &wizard.InitConfig{SourceURL: "https://github.com/example/x"}
	if err := resolvePin(context.Background(), cfg, true); err == nil {
		t.Fatal("expected offline error resolving a remote ref")
	}
}

func TestResolvePin_NothingToResolve(t *testing.T) {
	cfg := &wizard.InitConfig{} // no source at all
	if err := resolvePin(context.Background(), cfg, false); err != nil {
		t.Fatalf("resolvePin: %v", err)
	}
	if cfg.Commit != "" {
		t.Fatalf("expected no commit, got %q", cfg.Commit)
	}
}
