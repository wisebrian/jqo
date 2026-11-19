package jqo

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLocate(t *testing.T) {
	data := []byte("ab\ncd\r\nef\rgh é😀x")
	want := map[int]Position{
		0:  {0, 1, 1, 0},
		3:  {3, 2, 1, 3},   // after \n
		6:  {6, 2, 4, 6},   // the \n of \r\n
		7:  {7, 3, 1, 7},   // after \r\n, one line break
		10: {10, 4, 1, 10}, // after a lone \r
		13: {13, 4, 4, 13}, // é
		15: {15, 4, 5, 14}, // 😀: é took one UTF-16 unit
		19: {19, 4, 6, 16}, // x: 😀 took two
		20: {20, 4, 7, 17}, // the end
	}
	offs := []int{19, 0, 7, 20, 3, 15, 10, 13, 6} // any order
	pos := locate(data, offs)
	for i, off := range offs {
		if pos[i] != want[off] {
			t.Errorf("offset %d: got %+v, want %+v", off, pos[i], want[off])
		}
	}
	// An offset inside a character reports the character's start.
	if p := locate(data, []int{14})[0]; p.Column != 4 || p.Offset != 14 {
		t.Errorf("inside é: %+v", p)
	}
	// Invalid bytes count as one character each.
	if p := locate([]byte("a\xff\xfeb"), []int{3})[0]; p.Column != 4 || p.UTF16 != 3 {
		t.Errorf("after invalid bytes: %+v", p)
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("x", 100) + "!" + strings.Repeat("y", 100)
	for _, tc := range []struct {
		name, data string
		off        int
		text       string
		caret      string
	}{
		{"short", `{"a": 1 "b": 2}`, 8, `{"a": 1 "b": 2}`, "        ^"},
		{"second line", "[\n  1 2\n]", 6, "  1 2", "    ^"},
		{"crlf", "[\r\n  1 2\r\n]", 7, "  1 2", "    ^"},
		{"tab keeps alignment", "\t{\"a\" 1}", 6, "\t{\"a\" 1}", "\t     ^"},
		{"at end of line", "[1,\n", 3, "[1,", "   ^"},
		{"at end of input", "[1", 2, "[1", "  ^"},
		{"empty", "", 0, "", "^"},
		{"control character shown", "\"a\x01b\"", 2, "\"a␁b\"", "  ^"},
		{"invalid byte shown", "[\xff]", 1, "[�]", " ^"},
		{"no-break space shown", "{\"a\":\xc2\xa01}", 5, "{\"a\":␣1}", "     ^"},
		{"long line cut", long, 100, "…" + strings.Repeat("x", 40) + "!" + strings.Repeat("y", 39) + "…", " " + strings.Repeat(" ", 40) + "^"},
		{"wide characters count once", "[\"日本\" 1]", 10, "[\"日本\" 1]", "      ^"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, caret := snippet([]byte(tc.data), tc.off)
			if text != tc.text || caret != tc.caret {
				t.Errorf("got\n%q\n%q\nwant\n%q\n%q", text, caret, tc.text, tc.caret)
			}
			// The caret must sit under the character at off.
			if n := utf8.RuneCountInString(caret) - 1; n > utf8.RuneCountInString(text) {
				t.Errorf("caret beyond the text")
			}
		})
	}
}
