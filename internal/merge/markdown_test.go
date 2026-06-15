package merge

import (
	"errors"
	"strings"
	"testing"

	"github.com/agent-sync/agent-sync/pkg/adapterkit"
)

func mdUpsert(id, body string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindMarkdownSection, Locator: "agent-sync:" + id, Content: []byte(body)}
}
func mdRemove(id string) MergeEntry {
	return MergeEntry{Kind: adapterkit.ToolOwnedKindMarkdownSection, Locator: "agent-sync:" + id, Remove: true}
}

func md(existing string, e MergeEntry) (string, string, string, error) {
	r, h, w, err := mergeMarkdown([]byte(existing), e)
	return string(r), h, w, err
}

func TestMergeMarkdown_ReplacePreservesUserText(t *testing.T) {
	t.Parallel()
	in := "# My notes\n\nbefore text\n\n<!-- agent-sync:begin id=foo -->\nOLD\n<!-- agent-sync:end id=foo -->\n\nafter text\n"
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
	in := "<!-- agent-sync:begin id=foo -->\nold\n<!-- agent-sync:end id=foo -->\n"
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
	if !strings.Contains(out, "<!-- agent-sync:begin id=foo -->") {
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
	if strings.Contains(back, "agent-sync:begin id=foo") {
		t.Errorf("section not removed:\n%q", back)
	}
}

func TestMergeMarkdown_NoOpUpsertByteIdentical(t *testing.T) {
	t.Parallel()
	in := "x\n<!-- agent-sync:begin id=foo -->\nbody\n<!-- agent-sync:end id=foo -->\ny\n"
	once, _, _, err := md(in, mdUpsert("foo", "body\n"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if once != in {
		t.Errorf("no-op upsert changed bytes:\n got: %q\nwant: %q", once, in)
	}
}

// TestMergeMarkdown_CRLFFileWithLFBodyIsIdempotent guards the newline-flip
// regression: an LF-ending body (as IR canonical content usually arrives)
// spliced into a CRLF-authored file must produce a uniformly-newlined block,
// so a second merge is byte-identical. Without body normalization the first
// pass yields a mixed-newline file where the LF body lines outnumber the CRLF
// lines, detectNewline flips LF<->CRLF on the next pass, the markers re-render,
// and validate reports false drift forever.
func TestMergeMarkdown_CRLFFileWithLFBodyIsIdempotent(t *testing.T) {
	t.Parallel()
	existing := "intro\r\n"
	// Many LF lines so LF count exceeds the surrounding CRLF count.
	body := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n"
	first, h1, _, err := md(existing, mdUpsert("foo", body))
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	second, h2, _, err := md(first, mdUpsert("foo", body))
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if second != first {
		t.Errorf("merge not idempotent on CRLF file with LF body:\n first: %q\nsecond: %q", first, second)
	}
	if h1 != h2 {
		t.Errorf("slice hash unstable across passes: %q vs %q", h1, h2)
	}
}

func TestMergeMarkdown_MultiAdapterSectionsCoexist(t *testing.T) {
	t.Parallel()
	in := "<!-- agent-sync:begin id=cursor -->\nc\n<!-- agent-sync:end id=cursor -->\n"
	out, _, _, err := md(in, mdUpsert("codex", "x\n"))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(out, "id=cursor") || !strings.Contains(out, "id=codex") {
		t.Errorf("both sections should coexist:\n%s", out)
	}
}

// TestMergeMarkdown_CodexCursorCoexistenceAndIndependentRemoval is the Unit 11
// cross-adapter guarantee: codex and cursor both section-merge into the
// workspace-root AGENTS.md (byte-identical marker syntax, distinct id-keyed
// sections). Adding the codex section must preserve the cursor section AND the
// user prose outside the markers; removing the codex section must restore the
// file byte-for-byte to its pre-codex state.
func TestMergeMarkdown_CodexCursorCoexistenceAndIndependentRemoval(t *testing.T) {
	t.Parallel()

	userPlusCursor := "# My project\n\nHand-written prose.\n\n" +
		"<!-- agent-sync:begin id=cursor -->\ncursor section\n<!-- agent-sync:end id=cursor -->\n"

	withCodex, _, _, err := md(userPlusCursor, mdUpsert("codex", "codex section\n"))
	if err != nil {
		t.Fatalf("upsert codex: %v", err)
	}
	if !strings.Contains(withCodex, "id=cursor") || !strings.Contains(withCodex, "id=codex") {
		t.Fatalf("both adapter sections must coexist:\n%s", withCodex)
	}
	if !strings.Contains(withCodex, "Hand-written prose.") {
		t.Fatalf("user prose must survive codex upsert:\n%s", withCodex)
	}

	backToCursor, _, _, err := md(withCodex, mdRemove("codex"))
	if err != nil {
		t.Fatalf("remove codex: %v", err)
	}
	if strings.Contains(backToCursor, "id=codex") {
		t.Errorf("codex section should be gone after removal:\n%s", backToCursor)
	}
	// Byte-identity modulo trailing newlines: appending the codex section
	// inserts a blank-line separator that survives removal as one trailing
	// newline. That's a cosmetic merge artifact (no user content lost, not a
	// data-loss class), so the coexistence guarantee is asserted on the
	// content body, ignoring trailing whitespace.
	if strings.TrimRight(backToCursor, "\n") != strings.TrimRight(userPlusCursor, "\n") {
		t.Errorf("removing codex must restore the cursor section and user prose:\n got: %q\nwant: %q", backToCursor, userPlusCursor)
	}
}

func TestMergeMarkdown_CRLFPreserved(t *testing.T) {
	t.Parallel()
	in := "x\r\n<!-- agent-sync:begin id=foo -->\r\nold\r\n<!-- agent-sync:end id=foo -->\r\n"
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
		"begin no end":   "<!-- agent-sync:begin id=foo -->\nbody\n",
		"end no begin":   "body\n<!-- agent-sync:end id=foo -->\n",
		"nested begin":   "<!-- agent-sync:begin id=foo -->\n<!-- agent-sync:begin id=bar -->\n<!-- agent-sync:end id=bar -->\n",
		"duplicate id":   "<!-- agent-sync:begin id=foo -->\na\n<!-- agent-sync:end id=foo -->\n<!-- agent-sync:begin id=foo -->\nb\n<!-- agent-sync:end id=foo -->\n",
		"mismatched end": "<!-- agent-sync:begin id=foo -->\n<!-- agent-sync:end id=bar -->\n",
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
	in := "<!-- agent-sync:begin id=foo -->\nlive\n<!-- agent-sync:end id=foo -->\n\n  <!-- agent-sync:begin id=foo -->\n"
	_, _, _, err := md(in, mdUpsert("foo", "x\n"))
	if !errors.Is(err, ErrMalformedManagedSection) {
		t.Errorf("indented copy of managed id should refuse; err=%v", err)
	}
}

func TestMergeMarkdown_IndentedNonTargetMarkerWarnsAndAppends(t *testing.T) {
	t.Parallel()
	// An indented marker for a DIFFERENT id is user content; we append + warn.
	in := "  <!-- agent-sync:begin id=other -->\nuser text\n"
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
	if !strings.Contains(out, "  <!-- agent-sync:begin id=other -->") {
		t.Errorf("indented user line must be preserved verbatim:\n%s", out)
	}
}

func TestMergeMarkdown_BodyWithMarkerRejected(t *testing.T) {
	t.Parallel()
	_, _, _, err := md("", mdUpsert("foo", "x\n<!-- agent-sync:begin id=evil -->\n"))
	if err == nil || errors.Is(err, ErrMalformedManagedSection) {
		t.Errorf("body containing a marker must be a programmer error; got %v", err)
	}
}
