package codex

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
	if !strings.Contains(got, "{source-url}@{short-sha}") {
		t.Errorf("markdownHeader must keep the {source-url}@{short-sha} placeholder; got %q", got)
	}
}
