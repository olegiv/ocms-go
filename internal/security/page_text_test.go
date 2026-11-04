// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import "testing"

func TestPageHTMLText(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"blocks separate words", `<p>first</p><p>second</p>`, "first second"},
		{"inline joins words", `<p><strong>B</strong>old and <a href="/x">link</a>ed</p>`, "Bold and linked"},
		{"markup and styles are not text", `<p style="text-align: center"><span style="color: #e03e2d">Hi</span></p>`, "Hi"},
		{"entities decoded", `<p>caf&eacute; &amp; &lt;tag&gt;</p>`, "café & <tag>"},
		{"hidden elements dropped", `<p>a</p><script>alert(1)</script><style>p{}</style><iframe src="x">fallback</iframe><p>b</p>`, "a b"},
		{"breaks and images separate", `one<br>two<img src="/x.png" alt="alt text">three`, "one two three"},
		{"whitespace collapsed", "<p>\n  spaced \t out\n</p>", "spaced out"},
		{"control characters removed", "<p>a\x01b\x02c</p>", "abc"},
		{"table cells separate", `<table><tr><td>x</td><td>y</td></tr></table>`, "x y"},
		{"self-closing hidden elements hide their text", `<p>a</p><script/>secret code</script><p>visible</p>`, "a visible"},
		{"self-closing foreign element hides nothing", `<p>a</p><svg/><p>b</p>`, "a b"},
		{"unclosed svg does not hide the page", `<p>intro</p><svg><p>broken-out paragraph</p><p>more</p>`, "intro broken-out paragraph more"},
		{"svg text is visible text", `<svg><text>Chart label</text></svg>`, "Chart label"},
		{"metadata is not text", `<title>secret</title><p>shown</p><svg><title>tip</title><desc>hidden desc</desc><text>label</text></svg>`, "shown label"},
		{"legacy inline tags join words", `<p>NA<acronym>SA</acronym> <big>Big</big>ger <ruby>漢<rt>kan</rt></ruby></p>`, "NASA Bigger 漢kan"},
		{"word break and label join", `long<wbr>word <label>Name</label>:`, "longword Name:"},
		{"self-closing block separates", `one<br/>two`, "one two"},
		{"plain text unchanged", "already plain", "already plain"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PageHTMLText(tc.in); got != tc.want {
				t.Errorf("PageHTMLText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
