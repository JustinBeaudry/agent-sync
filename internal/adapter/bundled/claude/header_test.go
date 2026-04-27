package claude

import (
	"bytes"
	"strings"
	"testing"
)

func TestMarkdownHeader_ContainsManagedBanner(t *testing.T) {
	t.Parallel()

	got := string(markdownHeader())
	if !strings.Contains(got, "Managed by aienvs") {
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
	if !strings.Contains(body, "Managed by aienvs") {
		t.Errorf("jsonSidecarMarker missing managed banner; got %q", body)
	}
	if !strings.Contains(body, "aienvs unmanage claude") {
		t.Errorf("jsonSidecarMarker should name the unmanage exit path; got %q", body)
	}
	if !strings.Contains(body, ".mcp.json") {
		t.Errorf("jsonSidecarMarker should reference its sibling .mcp.json file; got %q", body)
	}
}

func TestSectionMarkers_ShapeAndID(t *testing.T) {
	t.Parallel()

	const id = "claude"
	begin := string(sectionMarkerBegin(id))
	end := string(sectionMarkerEnd(id))
	if want := "<!-- aienvs:begin id=claude -->"; begin != want {
		t.Errorf("sectionMarkerBegin = %q want %q", begin, want)
	}
	if want := "<!-- aienvs:end id=claude -->"; end != want {
		t.Errorf("sectionMarkerEnd = %q want %q", end, want)
	}
}

func TestSectionMarker_PanicsOnInvalidID(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("sectionMarkerBegin must panic on invalid id; got no panic")
		}
	}()
	_ = sectionMarkerBegin("../escape")
}

func TestWrapManagedSection_RoundsBodyWithMarkers(t *testing.T) {
	t.Parallel()

	got := wrapManagedSection("claude", []byte("## Build\nrun make"))
	want := "<!-- aienvs:begin id=claude -->\n## Build\nrun make\n<!-- aienvs:end id=claude -->\n"
	if string(got) != want {
		t.Errorf("wrapManagedSection mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestWrapManagedSection_HandlesTrailingNewlineInBody(t *testing.T) {
	t.Parallel()

	got := wrapManagedSection("claude", []byte("hello\n"))
	want := "<!-- aienvs:begin id=claude -->\nhello\n<!-- aienvs:end id=claude -->\n"
	if string(got) != want {
		t.Errorf("trailing newline duplicated:\n got: %q\nwant: %q", got, want)
	}
}

func TestWrapManagedSection_HandlesEmptyBody(t *testing.T) {
	t.Parallel()

	got := wrapManagedSection("claude", nil)
	if !bytes.HasPrefix(got, []byte("<!-- aienvs:begin id=claude -->\n")) {
		t.Errorf("empty body must still be wrapped with begin marker; got %q", got)
	}
	if !bytes.HasSuffix(got, []byte("<!-- aienvs:end id=claude -->\n")) {
		t.Errorf("empty body must still be closed with end marker; got %q", got)
	}
}

func TestReadmeForSubdir_NamesPathAndExit(t *testing.T) {
	t.Parallel()

	body := string(readmeForSubdir(".claude/rules/aienvs"))
	if !strings.Contains(body, ".claude/rules/aienvs") {
		t.Errorf("README must reference the subdir label; got %q", body)
	}
	if !strings.Contains(body, "aienvs unmanage claude") {
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
