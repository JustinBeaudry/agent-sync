package cli

import (
	"testing"

	"github.com/agent-sync/agent-sync/internal/manifest"
)

func boolPtr(b bool) *bool { return &b }

func TestShouldAutoAdvanceSync(t *testing.T) {
	t.Parallel()

	const sha = "1111111111111111111111111111111111111111"

	base := func() *manifest.Manifest {
		return &manifest.Manifest{
			Version: 1,
			Canonical: manifest.CanonicalSource{
				URL:    "https://example.com/agents.git",
				Ref:    "main",
				Commit: sha,
			},
			TrustedSHA: sha,
		}
	}

	cases := []struct {
		name    string
		mutate  func(*manifest.Manifest)
		frozen  bool
		offline bool
		want    bool
	}{
		{name: "url pinned with trusted_sha is eligible", want: true},
		{name: "frozen disables", frozen: true, want: false},
		{name: "offline disables", offline: true, want: false},
		{name: "auto:false disables", mutate: func(m *manifest.Manifest) { m.Canonical.Auto = boolPtr(false) }, want: false},
		{name: "auto:true stays eligible", mutate: func(m *manifest.Manifest) { m.Canonical.Auto = boolPtr(true) }, want: true},
		{name: "empty commit disables", mutate: func(m *manifest.Manifest) { m.Canonical.Commit = "" }, want: false},
		{
			name:   "empty trusted_sha disables (no anchor to advance)",
			mutate: func(m *manifest.Manifest) { m.TrustedSHA = "" },
			want:   false,
		},
		{
			name: "local_dir source is not eligible",
			mutate: func(m *manifest.Manifest) {
				m.Canonical = manifest.CanonicalSource{LocalDir: "agents", Commit: sha}
				m.TrustedSHA = sha
			},
			want: false,
		},
		{
			name: "local_path source is eligible",
			mutate: func(m *manifest.Manifest) {
				m.Canonical = manifest.CanonicalSource{LocalPath: "/tmp/clone", Ref: "main", Commit: sha}
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			if tc.mutate != nil {
				tc.mutate(m)
			}
			if got := shouldAutoAdvanceSync(m, tc.frozen, tc.offline); got != tc.want {
				t.Fatalf("shouldAutoAdvanceSync = %v, want %v", got, tc.want)
			}
		})
	}
}
