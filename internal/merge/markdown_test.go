package merge

import (
	"errors"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func mdUpsert(id, body string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindMarkdownSection, Locator: "aienvs:" + id, Content: []byte(body)}
}
func mdRemove(id string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindMarkdownSection, Locator: "aienvs:" + id, Remove: true}
}

func md(existing string, e MergeEntry) (string, string, string, error) {
	r, h, w, err := mergeMarkdown([]byte(existing), e)
	return string(r), h, w, err
}

func TestMergeMarkdown_ReplacePreservesUserText(t *testing.T) {
	t.Parallel()
	in := "# My notes\n\nbefore text\n\n<!-- aienvs:begin id=foo -->\nOLD\n<!-- aienvs:end id=foo -->\n\nafter text\n"
	out, hash, warn, err := md(in, mdUpsert("foo", "NEW BODY\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if hash == "" || warn != "" {
		t.Errorf("unexpected hash=%q warn=%q", hash, warn)
	}
	if !strings.Contains(out, "before text") || !strings.Contains(out, "after text") {
		t.Errorf("user text lost:\n%s", out)
	}
	if !strings.Contains(out, "NEW BODY") || strings.Contains(out, "OLD") {
		t.Errorf("managed content not replaced:\n%s", out)
	}
}

func TestMergeMarkdown_IdOnlyAdapterGrammarAccepted(t *testing.T) {
	t.Parallel()
	// The adapters emit id-only begin markers (no source=); they must parse.
	in := "<!-- aienvs:begin id=foo -->\nold\n<!-- aienvs:end id=foo -->\n"
	out, _, _, err := md(in, mdUpsert("foo", "new\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(out, "new") || strings.Contains(out, "old") {
		t.Errorf("id-only grammar not handled:\n%s", out)
	}
}

func TestMergeMarkdown_NewFileGetsHeader(t *testing.T) {
	t.Parallel()
	out, _, _, err := md("", mdUpsert("foo", "body\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(out, "Partially managed by agent-sync") {
		t.Errorf("new file missing managed header:\n%s", out)
	}
	if !strings.Contains(out, "<!-- aienvs:begin id=foo -->") {
		t.Errorf("new file missing section:\n%s", out)
	}
}

func TestMergeMarkdown_AppendKeepsUserContent(t *testing.T) {
	t.Parallel()
	in := "# user doc\n\nhello\n"
	out, _, _, err := md(in, mdUpsert("foo", "body\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.HasPrefix(out, in) {
		t.Errorf("user content not preserved as prefix:\n%s", out)
	}
}

func TestMergeMarkdown_UpsertThenRemoveIsIdentity(t *testing.T) {
	t.Parallel()
	in := "# doc\n\nhello\n"
	withFoo, _, _, err := md(in, mdUpsert("foo", "body\n"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	back, _, _, err := md(withFoo, mdRemove("foo"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Append adds one blank-line separator; remove drops only the block,
	// leaving the separator — assert user content intact and section gone.
	if !strings.HasPrefix(back, in) {
		t.Errorf("user content changed by upsert+remove:\n%q", back)
	}
	if strings.Contains(back, "aienvs:begin id=foo") {
		t.Errorf("section not removed:\n%q", back)
	}
}

func TestMergeMarkdown_NoOpUpsertByteIdentical(t *testing.T) {
	t.Parallel()
	in := "x\n<!-- aienvs:begin id=foo -->\nbody\n<!-- aienvs:end id=foo -->\ny\n"
	once, _, _, err := md(in, mdUpsert("foo", "body\n"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if once != in {
		t.Errorf("no-op upsert changed bytes:\n got: %q\nwant: %q", once, in)
	}
}

func TestMergeMarkdown_MultiAdapterSectionsCoexist(t *testing.T) {
	t.Parallel()
	in := "<!-- aienvs:begin id=cursor -->\nc\n<!-- aienvs:end id=cursor -->\n"
	out, _, _, err := md(in, mdUpsert("codex", "x\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(out, "id=cursor") || !strings.Contains(out, "id=codex") {
		t.Errorf("both sections should coexist:\n%s", out)
	}
}

func TestMergeMarkdown_CRLFPreserved(t *testing.T) {
	t.Parallel()
	in := "x\r\n<!-- aienvs:begin id=foo -->\r\nold\r\n<!-- aienvs:end id=foo -->\r\n"
	out, _, _, err := md(in, mdUpsert("foo", "new\r\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(out, "new\r\n") || strings.Count(out, "\r\n") < 3 {
		t.Errorf("CRLF newline style not preserved:\n%q", out)
	}
}

func TestMergeMarkdown_RecoveryRefusals(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"begin no end":   "<!-- aienvs:begin id=foo -->\nbody\n",
		"end no begin":   "body\n<!-- aienvs:end id=foo -->\n",
		"nested begin":   "<!-- aienvs:begin id=foo -->\n<!-- aienvs:begin id=bar -->\n<!-- aienvs:end id=bar -->\n",
		"duplicate id":   "<!-- aienvs:begin id=foo -->\na\n<!-- aienvs:end id=foo -->\n<!-- aienvs:begin id=foo -->\nb\n<!-- aienvs:end id=foo -->\n",
		"mismatched end": "<!-- aienvs:begin id=foo -->\n<!-- aienvs:end id=bar -->\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := md(in, mdUpsert("foo", "x\n"))
			if !errors.Is(err, ErrMalformedManagedSection) {
				t.Errorf("err=%v want ErrMalformedManagedSection", err)
			}
		})
	}
}

func TestMergeMarkdown_IndentedMarkerOfManagedIdRefused(t *testing.T) {
	t.Parallel()
	// An indented copy of the managed id must refuse, not append a duplicate.
	in := "<!-- aienvs:begin id=foo -->\nlive\n<!-- aienvs:end id=foo -->\n\n  <!-- aienvs:begin id=foo -->\n"
	_, _, _, err := md(in, mdUpsert("foo", "x\n"))
	if !errors.Is(err, ErrMalformedManagedSection) {
		t.Errorf("indented copy of managed id should refuse; err=%v", err)
	}
}

func TestMergeMarkdown_IndentedNonTargetMarkerWarnsAndAppends(t *testing.T) {
	t.Parallel()
	// An indented marker for a DIFFERENT id is user content; we append + warn.
	in := "  <!-- aienvs:begin id=other -->\nuser text\n"
	out, _, warn, err := md(in, mdUpsert("foo", "x\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if warn == "" {
		t.Error("expected a warning for the indented non-target marker")
	}
	if !strings.Contains(out, "id=foo") {
		t.Errorf("foo section should be appended:\n%s", out)
	}
	if !strings.Contains(out, "  <!-- aienvs:begin id=other -->") {
		t.Errorf("indented user line must be preserved verbatim:\n%s", out)
	}
}

func TestMergeMarkdown_BodyWithMarkerRejected(t *testing.T) {
	t.Parallel()
	_, _, _, err := md("", mdUpsert("foo", "x\n<!-- aienvs:begin id=evil -->\n"))
	if err == nil || errors.Is(err, ErrMalformedManagedSection) {
		t.Errorf("body containing a marker must be a programmer error; got %v", err)
	}
}
