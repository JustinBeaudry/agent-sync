package pi

import (
	"strings"
	"testing"
)

// TestRenderManagedHeader pins the three provenance forms of the managed
// banner. codex and pi carry an IDENTICAL copy of this table — the shared
// .agents/skills tree requires byte-identical co-emission (ADV-1), so a
// divergence in either package's renderer fails that package's own test.
func TestRenderManagedHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		url, commit string
		want        string
	}{
		{
			name:   "git-backed source renders url@short-sha",
			url:    "https://github.com/acme/agent-config",
			commit: "9cee577c8e96a8c859d37def58ab41584755c6da",
			want:   "<!-- Managed by agent-sync — do not edit. Source: https://github.com/acme/agent-config@9cee577c8e96. Regenerate: agent-sync sync -->\n\n",
		},
		{
			name: "local source (no commit) renders path only",
			url:  ".agents",
			want: "<!-- Managed by agent-sync — do not edit. Source: .agents. Regenerate: agent-sync sync -->\n\n",
		},
		{
			name: "no source identity omits the Source segment",
			want: "<!-- Managed by agent-sync — do not edit. Regenerate: agent-sync sync -->\n\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(renderManagedHeader(tc.url, tc.commit))
			if got != tc.want {
				t.Errorf("renderManagedHeader(%q, %q) = %q, want %q", tc.url, tc.commit, got, tc.want)
			}
			if strings.Contains(got, "{source-url}") || strings.Contains(got, "{short-sha}") {
				t.Errorf("rendered header contains template placeholders: %q", got)
			}
			if !strings.HasSuffix(got, "\n\n") {
				t.Errorf("header must end with a blank line; got tail %q", got)
			}
		})
	}
}

func TestShortSHA(t *testing.T) {
	t.Parallel()
	if got := shortSHA("9cee577c8e96a8c859d37def58ab41584755c6da"); got != "9cee577c8e96" {
		t.Errorf("shortSHA(full) = %q, want 12-char prefix", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("shortSHA(short input) = %q, want unchanged", got)
	}
}
