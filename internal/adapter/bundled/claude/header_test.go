package claude

import (
	"strings"
	"testing"
)

func TestMarkdownHeader_ContainsManagedBanner(t *testing.T) {
	t.Parallel()

	got := string(markdownHeader())
	if !strings.Contains(got, "Managed by agent-sync") {
		t.Errorf("markdownHeader missing canonical banner phrase; got %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("markdownHeader must end with a blank line; got tail %q", got[len(got)-4:])
	}
	// Source URL + short SHA are placeholders in v1; the placeholder
	// shape itself is part of the contract until Unit 13 plumbs real
	// values.
	if !strings.Contains(got, "{source-url}@{short-sha}") {
		t.Errorf("markdownHeader must keep the {source-url}@{short-sha} placeholder until Unit 13; got %q", got)
	}
}

func TestJSONSidecarMarker_NamesUnmanageCommand(t *testing.T) {
	t.Parallel()

	body := string(jsonSidecarMarker())
	if !strings.Contains(body, "Managed by agent-sync") {
		t.Errorf("jsonSidecarMarker missing managed banner; got %q", body)
	}
	if !strings.Contains(body, "agent-sync unmanage claude") {
		t.Errorf("jsonSidecarMarker should name the unmanage exit path; got %q", body)
	}
	if !strings.Contains(body, ".mcp.json") {
		t.Errorf("jsonSidecarMarker should reference its sibling .mcp.json file; got %q", body)
	}
}

func TestReadmeForSubdir_NamesPathAndExit(t *testing.T) {
	t.Parallel()

	body := string(readmeForSubdir(".claude/rules/agent-sync"))
	if !strings.Contains(body, ".claude/rules/agent-sync") {
		t.Errorf("README must reference the subdir label; got %q", body)
	}
	if !strings.Contains(body, "agent-sync unmanage claude") {
		t.Errorf("README must name the unmanage exit path; got %q", body)
	}
	if !strings.HasPrefix(body, "# ") {
		t.Errorf("README must start with a markdown heading; got %q", body[:min(40, len(body))])
	}
}

func TestReadmeForSubdir_DefaultsOnEmptyLabel(t *testing.T) {
	t.Parallel()

	body := string(readmeForSubdir(""))
	if !strings.Contains(body, "(unknown subdirectory)") {
		t.Errorf("empty subdir label should fall back to placeholder; got %q", body)
	}
}
