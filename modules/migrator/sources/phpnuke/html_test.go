// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"database/sql"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ns and ni build the nullable values the source models use, so tests read as
// close to plain literals as Go allows.
func ns(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
func ni(v int64) sql.NullInt64   { return sql.NullInt64{Int64: v, Valid: true} }

func TestAssembleStoryBodyJoinsBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name string
		home string
		body string
		want string
	}{
		{"both halves", "<p>Teaser</p>", "<p>Rest</p>", "<p>Teaser</p>\n\n<p>Rest</p>"},
		{"teaser only", "<p>Teaser</p>", "", "<p>Teaser</p>"},
		{"body only", "", "<p>Rest</p>", "<p>Rest</p>"},
		{"neither", "", "", ""},
		{"whitespace teaser", "   ", "<p>Rest</p>", "<p>Rest</p>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			story := &Story{
				HomeText: sql.NullString{String: tc.home, Valid: tc.home != ""},
				BodyText: ns(tc.body),
			}
			if got := assembleStoryBody(story); got != tc.want {
				t.Errorf("assembleStoryBody() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAssembleStoryBodyKeepsBodytextWhenHometextIsNull guards the split-body
// rule specifically: PHP-Nuke stores most of an article in bodytext, so
// dropping it would silently truncate the archive rather than fail.
func TestAssembleStoryBodyKeepsBodytextWhenHometextIsNull(t *testing.T) {
	story := &Story{
		HomeText: sql.NullString{Valid: false},
		BodyText: ns("<p>The entire article.</p>"),
	}
	if got := assembleStoryBody(story); !strings.Contains(got, "The entire article.") {
		t.Errorf("bodytext was dropped: got %q", got)
	}
}

func TestAssembleStaticPageBodyOrdersSections(t *testing.T) {
	page := &StaticPage{
		Header:    ns("<h1>Head</h1>"),
		Text:      ns("<p>Main</p>"),
		Footer:    ns("<p>Foot</p>"),
		Signature: ns("<em>Sig</em>"),
	}
	got := assembleStaticPageBody(page)
	want := "<h1>Head</h1>\n\n<p>Main</p>\n\n<p>Foot</p>\n\n<em>Sig</em>"
	if got != want {
		t.Errorf("assembleStaticPageBody() = %q, want %q", got, want)
	}

	sparse := &StaticPage{Text: ns("<p>Only body</p>")}
	if got := assembleStaticPageBody(sparse); got != "<p>Only body</p>" {
		t.Errorf("empty sections leaked separators: %q", got)
	}
}

func TestBuildEncyclopediaBodyRendersTerms(t *testing.T) {
	entry := &EncyclopediaEntry{Title: ns("Phrasebook"), Description: ns("<p>Intro</p>")}
	terms := []EncyclopediaTerm{
		{Title: ns("Привет!"), Text: ns("<p>Hello</p>")},
		{Title: ns("Как дела?"), Text: ns("<p>How are you</p>")},
	}
	got := buildEncyclopediaBody(entry, terms)

	for _, want := range []string{"<p>Intro</p>", "<dl>", "<dt>Привет!</dt>", "<dd><p>Hello</p></dd>", "</dl>"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q\ngot: %s", want, got)
		}
	}
}

// TestBuildEncyclopediaBodyEscapesTermTitles proves term titles cannot inject
// markup: only the term body is trusted HTML from the source site.
func TestBuildEncyclopediaBodyEscapesTermTitles(t *testing.T) {
	entry := &EncyclopediaEntry{Title: ns("E")}
	terms := []EncyclopediaTerm{{Title: ns(`<img src=x onerror=alert(1)>`), Text: ns("ok")}}
	got := buildEncyclopediaBody(entry, terms)
	if strings.Contains(got, "<img src=x") {
		t.Errorf("term title was not escaped: %s", got)
	}
	if !strings.Contains(got, "&lt;img") {
		t.Errorf("expected escaped title, got: %s", got)
	}
}

func TestBuildEncyclopediaBodyWithoutTermsOmitsList(t *testing.T) {
	entry := &EncyclopediaEntry{Title: ns("Empty"), Description: ns("<p>Nothing here</p>")}
	got := buildEncyclopediaBody(entry, nil)
	if strings.Contains(got, "<dl>") {
		t.Errorf("empty encyclopedia emitted a definition list: %q", got)
	}
	if got != "<p>Nothing here</p>" {
		t.Errorf("got %q", got)
	}
}

func TestDeriveSummaryStripsMarkupAndCollapsesWhitespace(t *testing.T) {
	got := deriveSummary("<p>Hello   <b>there</b>\n\nworld</p><script>evil()</script>")
	want := "Hello there world"
	if got != want {
		t.Errorf("deriveSummary() = %q, want %q", got, want)
	}
}

func TestDeriveSummaryTruncatesOnRuneBoundary(t *testing.T) {
	// Cyrillic is multi-byte, so a naive byte slice would split a rune and
	// produce invalid UTF-8.
	long := strings.Repeat("зеленый ", 100)
	got := deriveSummary(long)

	if !utf8.ValidString(got) {
		t.Fatalf("summary is not valid UTF-8: %q", got)
	}
	if runes := utf8.RuneCountInString(got); runes > summaryLimit+1 {
		t.Errorf("summary is %d runes, want <= %d", runes, summaryLimit+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated summary should end with an ellipsis: %q", got)
	}
}

// TestPlainTextNeutralizesEntityEncodedMarkup covers a real audit finding
// (F-02): the tokenizer decodes entities in text nodes, so source text that
// merely *displayed* as "<script>" on the old site came back as a genuine
// "<script>" substring in the stored summary. Harmless while these fields are
// auto-escaped, and a live hazard the moment one reaches a raw-HTML sink.
func TestPlainTextNeutralizesEntityEncodedMarkup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fragment string
		want     string
	}{
		{"entity-encoded script", "<p>&lt;script&gt;alert(1)&lt;/script&gt; hello</p>", "hello"},
		{"entity-encoded tag", "<p>a &lt;b&gt; c</p>", "a c"},
		{"real markup", "<p>x <b>y</b> z</p>", "x y z"},
		// Doubly encoded input decodes to "&lt;script&gt;" and stops there,
		// which is correct: a browser decodes entities once, so that string is
		// inert text even in a raw sink. Decoding repeatedly would be the bug.
		{"doubly encoded stops at inert entities", "<p>&amp;lt;script&amp;gt;</p>", "&lt;script&gt;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := plainText(tc.fragment)
			if got != tc.want {
				t.Errorf("plainText() = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "<>") {
				t.Errorf("result still contains angle brackets: %q", got)
			}
		})
	}
}

// TestPlainTextKeepsLoneAngleBrackets guards the other direction: a blunt
// strip-everything-bracketed rule would corrupt ordinary prose. The tokenizer
// only opens a tag when "<" is followed by a name character.
func TestPlainTextKeepsLoneAngleBrackets(t *testing.T) {
	if got := plainText("<p>5 &lt; 10 and 20 &gt; 3</p>"); got != "5 < 10 and 20 > 3" {
		t.Errorf("plainText() = %q, want the comparison text intact", got)
	}
}

func TestPlainTextPreservesCyrillic(t *testing.T) {
	if got := plainText("<p>Отель &lt;b&gt;Royal&lt;/b&gt; Azur</p>"); got != "Отель Royal Azur" {
		t.Errorf("plainText() = %q", got)
	}
}

// TestPlainTextTerminates proves the re-extraction loop is bounded and cannot
// spin on adversarial nesting.
func TestPlainTextTerminates(t *testing.T) {
	done := make(chan string, 1)
	go func() { done <- plainText(strings.Repeat("&amp;", 200) + "lt;script&gt;") }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("plainText did not terminate")
	}
}

func TestDeriveSummaryStripsEntityEncodedMarkup(t *testing.T) {
	got := deriveSummary("<p>&lt;img src=x onerror=alert(1)&gt; Отель</p>")
	if strings.ContainsAny(got, "<>") {
		t.Errorf("summary retained markup characters: %q", got)
	}
	if !strings.Contains(got, "Отель") {
		t.Errorf("summary lost its real text: %q", got)
	}
}

func TestDeriveSummaryLeavesShortTextIntact(t *testing.T) {
	if got := deriveSummary("<p>Отель Royal Azur</p>"); got != "Отель Royal Azur" {
		t.Errorf("deriveSummary() = %q", got)
	}
}

func TestExtractAssetRefsFindsLocalImages(t *testing.T) {
	body := `
		<img src="tourism/hotels/ra_boat_0_prv.jpg" alt="boat">
		<img src="/tourism/places/eljem/10070007.jpg">
		<a href="docs/brochure.pdf">Brochure</a>
	`
	refs := extractAssetRefs(body)

	paths := make(map[string]string, len(refs))
	for _, ref := range refs {
		paths[ref.Raw] = ref.Path
	}
	want := map[string]string{
		"tourism/hotels/ra_boat_0_prv.jpg":   "tourism/hotels/ra_boat_0_prv.jpg",
		"/tourism/places/eljem/10070007.jpg": "tourism/places/eljem/10070007.jpg",
		"docs/brochure.pdf":                  "docs/brochure.pdf",
	}
	if len(paths) != len(want) {
		t.Fatalf("got %d refs (%v), want %d", len(paths), paths, len(want))
	}
	for raw, wantPath := range want {
		if paths[raw] != wantPath {
			t.Errorf("ref %q -> %q, want %q", raw, paths[raw], wantPath)
		}
	}
}

// TestExtractAssetRefsKeepsRawAndNormalizedApart is the reason the two fields
// exist: the same file is written with and without a leading slash across a
// PHP-Nuke site, and the body must be rewritten using the exact text it holds.
func TestExtractAssetRefsKeepsRawAndNormalizedApart(t *testing.T) {
	refs := extractAssetRefs(`<img src="a/b.jpg"><img src="/a/b.jpg">`)
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	for _, ref := range refs {
		if ref.Path != "a/b.jpg" {
			t.Errorf("normalized path = %q, want %q", ref.Path, "a/b.jpg")
		}
	}
	if refs[0].Raw == refs[1].Raw {
		t.Error("raw attribute text was collapsed; body rewriting would miss one form")
	}
}

func TestExtractAssetRefsIgnoresNonLocalAndNonMedia(t *testing.T) {
	body := `
		<img src="http://example.com/x.jpg">
		<img src="https://example.com/y.jpg">
		<img src="//cdn.example.com/z.jpg">
		<img src="data:image/gif;base64,R0lGOD">
		<a href="modules.php?name=News&amp;file=article&amp;sid=12">Story</a>
		<a href="#anchor">Anchor</a>
		<a href="mailto:someone@example.com">Mail</a>
		<img src="../secrets/passwd.jpg">
		<a href="notes.txt">Notes</a>
		<a href="index.php">Home</a>
	`
	if refs := extractAssetRefs(body); len(refs) != 0 {
		t.Errorf("expected no local media refs, got %v", refs)
	}
}

func TestNormalizeAssetPath(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		wantPath string
		wantOK   bool
	}{
		{"images/a.jpg", "images/a.jpg", true},
		{"/images/a.jpg", "images/a.jpg", true},
		{"images/a.PNG", "images/a.PNG", true},
		{"a.pdf", "a.pdf", true},
		{"", "", false},
		{"   ", "", false},
		{"/", "", false},
		{"http://x/a.jpg", "", false},
		{"//x/a.jpg", "", false},
		{"data:image/png;base64,AAAA", "", false},
		{"a/../b.jpg", "", false},
		{"a//b.jpg", "", false},
		{"./a.jpg", "", false},
		{"a.jpg?v=2", "", false},
		{"a.jpg#frag", "", false},
		{"script.php", "", false},
		{"noextension", "", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := normalizeAssetPath(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("normalizeAssetPath(%q) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if ok && got != tc.wantPath {
				t.Errorf("normalizeAssetPath(%q) = %q, want %q", tc.raw, got, tc.wantPath)
			}
		})
	}
}

func TestExtractAssetRefsIsDeterministic(t *testing.T) {
	body := `<img src="c.jpg"><img src="a.jpg"><img src="b.jpg">`
	first := extractAssetRefs(body)
	for i := 0; i < 5; i++ {
		again := extractAssetRefs(body)
		if len(again) != len(first) {
			t.Fatalf("ref count changed between runs")
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("ref order changed between runs at %d: %v vs %v", j, again[j], first[j])
			}
		}
	}
	if first[0].Path != "a.jpg" {
		t.Errorf("refs are not sorted: %v", first)
	}
}

// TestMarkupRemovedIgnoresRewrites covers a second-pass review finding. The
// sanitizer adds rel="nofollow" to every link and normalizes entities, so a
// plain "output != input" check counted almost every article body while the
// summary claimed markup had been *removed* — the opposite of what happened.
func TestMarkupRemovedIgnoresRewrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before string
		after  string
		want   bool
	}{
		{"nofollow added", `<a href="/x">l</a>`, `<a href="/x" rel="nofollow">l</a>`, false},
		{"entity normalized", `<p>caf&eacute;</p>`, `<p>café</p>`, false},
		{"identical", `<p>hi</p>`, `<p>hi</p>`, false},
		{"element dropped", `<font color="red">x</font>`, `x`, true},
		{"attribute dropped", `<p style="color:red">x</p>`, `<p>x</p>`, true},
		{"one of several dropped", `<p><b>a</b><iframe src="x"></iframe></p>`, `<p><b>a</b></p>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markupRemoved(tc.before, tc.after); got != tc.want {
				t.Errorf("markupRemoved() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPrepareBodyTallyCountsOnlyRemovals runs the real sanitizer, so it pins
// the behaviour against the policy rather than against a hand-written fixture.
func TestPrepareBodyTallyCountsOnlyRemovals(t *testing.T) {
	source := NewSource()

	linkOnly := 0
	source.prepareBody(`<p><a href="/x.html">a link</a> and caf&eacute;</p>`, nil, &linkOnly)
	if linkOnly != 0 {
		t.Errorf("a body the sanitizer only rewrote was counted as losing markup (%d)", linkOnly)
	}

	legacy := 0
	source.prepareBody(`<font color="red">legacy</font>`, nil, &legacy)
	if legacy != 1 {
		t.Errorf("a body that lost <font> was not counted (%d)", legacy)
	}
}

// TestPlainTextResistsEncodingPump covers a review finding. The loop used to
// key on "contains any angle bracket", so a bare ">" — ordinary punctuation —
// kept it running while each pass peeled one layer off an encoded payload. The
// value returned after the final pass then held a live tag, in a function whose
// entire contract is that it does not.
func TestPlainTextResistsEncodingPump(t *testing.T) {
	payload := "&lt;script&gt;alert(1)&lt;/script&gt;"
	for i := 0; i < maxPlainTextPasses+3; i++ {
		payload = strings.ReplaceAll(payload, "&", "&amp;")
	}
	got := plainText("&gt; " + payload)

	if containsTag(got) {
		t.Errorf("output holds a live tag: %q", got)
	}
	if strings.Contains(got, "<script") {
		t.Errorf("output holds a script tag: %q", got)
	}
}

// TestPlainTextNeverReturnsATag is the general form: whatever the input, the
// result must not tokenize as markup. The cap is not a safety boundary on its
// own, so the terminal guard has to hold for input engineered to exceed it.
func TestPlainTextNeverReturnsATag(t *testing.T) {
	deep := "<img src=x onerror=alert(1)>"
	for i := 0; i < 20; i++ {
		deep = strings.ReplaceAll(strings.ReplaceAll(deep, "&", "&amp;"), "<", "&lt;")
		deep = strings.ReplaceAll(deep, ">", "&gt;")
	}
	for _, in := range []string{
		"&gt; " + deep,
		"&lt;&lt;&lt;script&gt;&gt;&gt;",
		strings.Repeat("&gt;", 50) + "&lt;script&gt;",
	} {
		if got := plainText(in); containsTag(got) {
			t.Errorf("plainText(%.40q…) returned markup: %q", in, got)
		}
	}
}

func TestContainsTagIgnoresLonePunctuation(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"5 < 10 and 20 > 3", false},
		{"a > b", false},
		{"&lt;script&gt;", false},
		{"<script>", true},
		{"</p>", true},
		{"<br/>", true},
	} {
		if got := containsTag(tc.in); got != tc.want {
			t.Errorf("containsTag(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeAssetPathDecodesPercentEscapes covers a Codex review finding. A
// path is written as a URL but opened as a filename, so "My%20Photo.jpg" names
// a file called "My Photo.jpg". Without decoding, the file was reported missing
// and the old reference stayed in the imported body.
func TestNormalizeAssetPathDecodesPercentEscapes(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		wantPath string
		wantOK   bool
	}{
		{"images/My%20Photo.jpg", "images/My Photo.jpg", true},
		{"images/%D0%9E%D1%82%D0%B5%D0%BB%D1%8C.jpg", "images/Отель.jpg", true},
		{"images/plain.jpg", "images/plain.jpg", true},
		// Decoding happens before validation, so encoded traversal is caught.
		{"images/%2e%2e%2fsecret.jpg", "", false},
		{"%2fetc%2fpasswd.jpg", "", false},
		{"images/bad%zz.jpg", "", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := normalizeAssetPath(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("normalizeAssetPath(%q) ok = %v, want %v (got %q)", tc.raw, ok, tc.wantOK, got)
			}
			if ok && got != tc.wantPath {
				t.Errorf("normalizeAssetPath(%q) = %q, want %q", tc.raw, got, tc.wantPath)
			}
		})
	}
}

// TestExtractAssetRefsKeepsEscapedSpelling covers a Codex review finding. The
// tokenizer entity-decodes attribute values, so a body holding
// src="a&amp;b.jpg" yields "a&b.jpg" — a spelling that does not appear in the
// body, so rewriting silently found nothing and left the legacy path in place.
func TestExtractAssetRefsKeepsEscapedSpelling(t *testing.T) {
	refs := extractAssetRefs(`<img src="images/a&amp;b.jpg">`)

	spellings := make(map[string]string, len(refs))
	for _, ref := range refs {
		spellings[ref.Raw] = ref.Path
	}
	if _, ok := spellings["images/a&amp;b.jpg"]; !ok {
		t.Errorf("the escaped spelling that actually appears in the body is missing: %v", spellings)
	}
	if _, ok := spellings["images/a&b.jpg"]; !ok {
		t.Errorf("the decoded spelling is missing: %v", spellings)
	}
	for raw, path := range spellings {
		if path != "images/a&b.jpg" {
			t.Errorf("ref %q resolved to %q, want the decoded filesystem path", raw, path)
		}
	}
}

// TestTopicLabelIgnoresWhitespaceOnlyText covers a Codex review finding: a
// whitespace-only topictext was returned as the label, creating a visually
// blank category while a usable topicname sat unused.
func TestTopicLabelIgnoresWhitespaceOnlyText(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"rHotels", "   ", "rHotels"},
		{"rHotels", "\t\n ", "rHotels"},
		{"rHotels", "Отели", "Отели"},
		{"rHotels", "", "rHotels"},
		{"  ", "  ", ""},
	} {
		topic := &Topic{Name: ns(tc.name), Text: ns(tc.text)}
		if got := topic.Label(); got != tc.want {
			t.Errorf("Label() with name=%q text=%q = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}
