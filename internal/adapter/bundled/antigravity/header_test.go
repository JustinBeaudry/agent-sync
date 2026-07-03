package antigravity

import (
	"strings"
	"testing"
)

func TestRenderManagedHeader_Forms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		url    string
		commit string
		want   string
	}{
		{
			name: "no source",
			want: "<!-- Managed by agent-sync — do not edit. Regenerate: agent-sync sync -->\n\n",
		},
		{
			name: "local source",
			url:  "/local/path",
			want: "<!-- Managed by agent-sync — do not edit. Source: /local/path. Regenerate: agent-sync sync -->\n\n",
		},
		{
			name:   "git-backed truncates sha to 12",
			url:    "https://example.com/repo",
			commit: "0123456789abcdef0123456789",
			want:   "<!-- Managed by agent-sync — do not edit. Source: https://example.com/repo@0123456789ab. Regenerate: agent-sync sync -->\n\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := string(renderManagedHeader(tc.url, tc.commit)); got != tc.want {
				t.Errorf("renderManagedHeader(%q,%q) =\n %q\nwant\n %q", tc.url, tc.commit, got, tc.want)
			}
		})
	}
}

func TestReadmeForSubdir_MentionsUnmanage(t *testing.T) {
	t.Parallel()

	body := string(readmeForSubdir(rulesSubdir))
	if !strings.Contains(body, "agent-sync unmanage antigravity") {
		t.Errorf("README missing unmanage instruction; got %q", body)
	}
	if !strings.Contains(body, rulesSubdir) {
		t.Errorf("README missing subdir label %q; got %q", rulesSubdir, body)
	}
}
