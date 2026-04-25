package trust

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPromptFirstURLAccepts(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	in := strings.NewReader("yes\n")
	p := NewPrompter(in, &out).WithNoColor()

	ok, err := p.ConfirmFirstURL(urlX, shaA)
	if err != nil {
		t.Fatalf("ConfirmFirstURL: %v", err)
	}
	if !ok {
		t.Error("expected accept on yes input")
	}
	if !strings.Contains(out.String(), urlX) {
		t.Errorf("prompt output missing URL: %q", out.String())
	}
	if !strings.Contains(out.String(), shaA[:12]) {
		t.Errorf("prompt output missing short SHA: %q", out.String())
	}
}

func TestPromptFirstURLDeclines(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	in := strings.NewReader("no\n")
	p := NewPrompter(in, &out).WithNoColor()

	ok, err := p.ConfirmFirstURL(urlX, shaA)
	if err != nil {
		t.Fatalf("ConfirmFirstURL: %v", err)
	}
	if ok {
		t.Error("expected decline on no input")
	}
}

func TestPromptFirstURLEOF(t *testing.T) {
	t.Parallel()

	// EOF before any input is a decline (safer default than "yes on empty").
	var out bytes.Buffer
	p := NewPrompter(strings.NewReader(""), &out).WithNoColor()

	ok, err := p.ConfirmFirstURL(urlX, shaA)
	if err != nil {
		t.Fatalf("ConfirmFirstURL: %v", err)
	}
	if ok {
		t.Error("expected decline on EOF")
	}
}

func TestPromptNewSHA(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	in := strings.NewReader("y\n")
	p := NewPrompter(in, &out).WithNoColor()

	ok, err := p.ConfirmNewSHA(urlX, shaB, shaA)
	if err != nil {
		t.Fatalf("ConfirmNewSHA: %v", err)
	}
	if !ok {
		t.Error("expected accept on y input")
	}
	// Output should name both old and new short SHAs.
	s := out.String()
	if !strings.Contains(s, shaA[:12]) || !strings.Contains(s, shaB[:12]) {
		t.Errorf("prompt output missing short SHAs: %q", s)
	}
}

func TestPromptRevokedBannerNoPrompt(t *testing.T) {
	t.Parallel()

	// Revoked banner MUST NOT prompt. It renders the banner and returns
	// ErrRevokedTrustAnchor. Even with stdin full of "yes\n" we don't read it.
	var out bytes.Buffer
	in := strings.NewReader("yes\n")
	p := NewPrompter(in, &out).WithNoColor()

	err := p.RenderRevokedBanner(urlX)
	if !errors.Is(err, ErrRevokedTrustAnchor) {
		t.Fatalf("RenderRevokedBanner err = %v, want ErrRevokedTrustAnchor", err)
	}

	s := out.String()
	if !strings.Contains(s, urlX) {
		t.Errorf("banner missing URL: %q", s)
	}
	if !strings.Contains(s, "aienvs trust reset") {
		t.Errorf("banner missing remediation: %q", s)
	}

	// Stdin untouched — next read still returns the full buffer.
	remaining := make([]byte, 8)
	n, _ := in.Read(remaining)
	if n != len("yes\n") {
		t.Errorf("RenderRevokedBanner consumed %d bytes of stdin, want 0", len("yes\n")-n)
	}
}

func TestPromptTypedConfirmation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  string
		expect string
		wantOK bool
	}{
		{"exact match", "github.com/foo/bar\n", "github.com/foo/bar", true},
		{"wrong value", "yes\n", "github.com/foo/bar", false},
		{"empty", "\n", "github.com/foo/bar", false},
		{"trailing spaces trimmed", "  github.com/foo/bar  \n", "github.com/foo/bar", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			p := NewPrompter(strings.NewReader(tc.input), &out).WithNoColor()
			ok, err := p.TypedConfirmation("delete", tc.expect)
			if err != nil {
				t.Fatalf("TypedConfirmation: %v", err)
			}
			if ok != tc.wantOK {
				t.Errorf("TypedConfirmation = %v, want %v (input=%q)", ok, tc.wantOK, tc.input)
			}
		})
	}
}

func TestPromptColorSuppressedWithNoColor(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("no\n"), &out).WithNoColor()

	_ = p.RenderRevokedBanner(urlX)
	s := out.String()
	if strings.Contains(s, "\x1b[") {
		t.Errorf("NO_COLOR surface emitted ANSI escape: %q", s)
	}
}
