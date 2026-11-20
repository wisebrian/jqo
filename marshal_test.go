package jqo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppendJSONString(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:          `"plain"`,
		`a"b\c`:          `"a\"b\\c"`,
		"tab\tnl\ncr\r":  `"tab\tnl\ncr\r"`,
		"\x01\x1f":       `"\u0001\u001f"`,
		"é😀 ":            "\"é😀 \"",
		"\xed\xa0\x80":   `"\ud800"`, // WTF-8 for a lone surrogate
		"x\xed\xbf\xbfy": `"x\udfffy"`,
		"\xff":           "\"\\ufffd\"",
		"\xed\xa0":       "\"\\ufffd\\ufffd\"", // truncated, not a surrogate
	} {
		got := string(appendJSONString(nil, in))
		if got != want {
			t.Errorf("appendJSONString(%q) = %s, want %s", in, got, want)
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("%s is not valid JSON", got)
		}
	}
}

func TestMarshalReport(t *testing.T) {
	rep, err := Parse([]byte("{\"a\": 1, \"a\": 2, // x\n \"n\": 12345678901234567890,}"), Options{Lenient: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rep.MarshalJSON()
	if err != nil || !json.Valid(b) {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
	var got struct {
		Summary struct {
			Size, MaxDepth, Objects, Numbers, Keys int
		}
		Warnings []struct {
			Code    string
			Message string
			Pos     Position
			First   *Position
		}
		WarningCount int
		Fixes        []struct {
			Code     string
			Pos, End Position
		}
		FixCount int
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary.Size != rep.Summary.Size || got.Summary.Keys != 3 || got.WarningCount != 2 || got.FixCount != 2 {
		t.Errorf("summary or counts wrong: %s", b)
	}
	if got.Warnings[0].Code != "duplicate-key" || got.Warnings[0].First == nil || *got.Warnings[0].First != rep.Warnings[0].First {
		t.Errorf("duplicate warning: %s", b)
	}
	if got.Warnings[1].First != nil || got.Warnings[1].Pos != rep.Warnings[1].Pos {
		t.Errorf("integer warning: %s", b)
	}
	if got.Fixes[0].Code != "comment" || got.Fixes[0].End != rep.Fixes[0].End {
		t.Errorf("fixes: %s", b)
	}
}

func TestMarshalError(t *testing.T) {
	_, err := Parse([]byte("[1 2]"), Options{})
	b, _ := asError(t, err).MarshalJSON()
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "expected-comma-or-close" || got["snippet"] != "[1 2]" || got["caret"] != "   ^" ||
		!strings.HasPrefix(got["text"].(string), "Line 1, column 4: ") || got["pos"].(map[string]any)["utf16"] != 3.0 {
		t.Errorf("got %s", b)
	}
}
