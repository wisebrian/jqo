package jqo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func joinLines(ls ...string) string { return strings.Join(ls, "\n") }

// diagnosed scans data as Parse does and returns its first error as the
// scanner found it and as diagnose moved it, with the fix diagnose proposes.
// All are zero for valid input.
func diagnosed(data []byte, lenient bool) (raw, moved *Error, f fix) {
	s := newScanner(data, lenient, DefaultMaxDepth)
	for {
		switch s.next().kind {
		case tokEOF:
			return nil, nil, fix{}
		case tokError:
			m, f := diagnose(data, lenient, DefaultMaxDepth, s.err)
			raw = finish(data, &Error{Code: s.err.Code, Message: s.err.Message, Pos: Position{Offset: s.err.Pos.Offset}})
			moved = finish(data, &Error{Code: m.Code, Message: m.Message, Pos: Position{Offset: m.Pos.Offset}})
			return raw, moved, f
		}
	}
}

// applyFix makes the edit f at off.
func applyFix(data []byte, off int, f fix) []byte {
	out := append([]byte(nil), data[:off]...)
	out = append(out, f.ins...)
	return append(out, data[off+f.del:]...)
}

// checkFix fails unless the fix diagnose proposed at e makes data valid.
func checkFix(t *testing.T, data []byte, lenient bool, e *Error, f fix) {
	t.Helper()
	fixed := applyFix(data, e.Pos.Offset, f)
	if _, err := Parse(fixed, Options{Lenient: lenient}); err != nil {
		t.Errorf("the proposed fix %+v at %d:%d leaves %v in\n%s", f, e.Pos.Line, e.Pos.Column, err, fixed)
	}
}

// Each case is a mistake the error should point at, rather than at the place
// parsing stopped; the fix the diagnosis proposes there must make the input
// valid.
var diagnoseCases = []struct {
	name      string
	in        string
	lenient   bool
	code      Code
	line, col int
	msg       string
}{
	{"comma between keys", joinLines(`{`, `  "name": "dimih",`, `  "tools": 2`, `  "since": 2025`, `}`), false,
		CodeExpectedCommaOrClose, 3, 13, "a comma is missing here, before the key at line 4, column 3"},
	{"comma between values", joinLines(`[`, `  "qr",`, `  "json"`, `  "pdf"`, `]`), false,
		CodeExpectedCommaOrClose, 3, 9, "a comma is missing here, before the value at line 4, column 3"},
	{"comma after a nested object", joinLines(`{`, `  "owner": {`, `    "name": "mihai"`, `  }`, `  "since": 2025`, `}`), false,
		CodeExpectedCommaOrClose, 4, 4, "before the key at line 5, column 3"},
	{"comma between objects in an array", joinLines(`{`, `  "users": [`, `    {"id": 1}`, `    {"id": 2}`, `  ]`, `}`), false,
		CodeExpectedCommaOrClose, 3, 14, "before the value at line 4, column 5"},
	{"comma with CRLF", "{\r\n  \"name\": \"dimih\",\r\n  \"tools\": 2\r\n  \"since\": 2025\r\n}", false,
		CodeExpectedCommaOrClose, 3, 13, "before the key at line 4, column 3"},
	{"comma with tabs and diacritics", "{\n\t\"nume\": \"Mădălina\"\n\t\"oraș\": \"Iași\"\n}", false,
		CodeExpectedCommaOrClose, 2, 20, "before the key at line 3, column 2"},
	{"comma before a comment", joinLines(`{`, `  "a": 1 // first`, `  "b": 2`, `}`), true,
		CodeExpectedCommaOrClose, 2, 9, "before the key at line 3, column 3"},
	{"comma between unindented values", joinLines(`[`, `{"a": 1}`, `{"b": 2}`, `]`), false,
		CodeExpectedCommaOrClose, 2, 9, "before the value at line 3, column 1"},
	{"comma before a key at the outer level", joinLines(`{`, `  "a": 1`, `"b": 2`, `}`), false,
		CodeExpectedCommaOrClose, 2, 9, "before the key at line 3, column 1"},
	{"} before a sibling key", joinLines(`{`, `  "owner": {`, `    "name": "mihai",`, `    "city": "x",`, `  "since": 2025`, `}`), false,
		CodeMissingBracket, 4, 16, "the object that starts at line 2, column 12 is not closed: a } is missing here; the } at line 6, column 1 closes it instead"},
	{"} in an array of objects", joinLines(`[`, `  {"name": "qr", "lib": "gqr"},`, `  {"name": "json", "lib": "jqo",`, `  {"name": "pdf", "lib": "pdfo"}`, `]`), false,
		CodeMissingBracket, 3, 32, "the object that starts at line 3, column 3 is not closed: a } is missing here; parsing stopped at line 4, column 3"},
	{"] before a sibling key", joinLines(`{`, `  "tools": ["qr", "json",`, `  "since": 2025`, `}`), false,
		CodeMissingBracket, 2, 25, "the array that starts at line 2, column 12 is not closed: a ] is missing here; parsing stopped at line 3, column 10"},
	{"] and a comma before a sibling key", joinLines(`{`, `  "a": [`, `    1,`, `    2`, `  "b": 3`, `}`), false,
		CodeMissingBracket, 4, 6, "the array that starts at line 2, column 8 is not closed: a ] is missing here, and a comma after it; parsing stopped at line 5, column 3"},
	{"} before the outer ]", joinLines(`{`, `  "list": [`, `    {"a": 1},`, `    {"b": 2`, `  ],`, `  "c": 3`, `}`), false,
		CodeMissingBracket, 4, 12, "the object that starts at line 4, column 5 is not closed: a } is missing here; parsing stopped at line 5, column 3"},
	{"two } before a sibling key", joinLines(`{`, `  "a": {`, `    "b": {`, `      "c": 1,`, `  "d": 2`, `}`), false,
		CodeMissingBracket, 4, 13, "the object that starts at line 3, column 10 and the object that starts at line 2, column 8 are not closed: }} are missing here; the } at line 6, column 1 closes the first instead"},
	{"} with a comment between", joinLines(`{`, `  "owner": {`, `    "name": "mihai", // who`, `  /* where */`, `  "since": 2025`, `}`), true,
		CodeMissingBracket, 3, 20, "the object that starts at line 2, column 12 is not closed"},
	{"{ before a key", joinLines(`{`, `  "owner":`, `    "name": "mihai"`, `  },`, `  "since": 2025`, `}`), false,
		CodeMissingBracket, 3, 5, `"name" is followed by a colon, like a key: is a { missing before it?`},
	{"} too many", joinLines(`{`, `  "owner": {"name": "mihai"}},`, `  "since": 2025`, `}`), false,
		CodeExtraData, 2, 29, "this } closes the whole JSON value, but a key follows at line 3, column 3: is the } one too many?"},
}

func TestDiagnose(t *testing.T) {
	for _, tc := range diagnoseCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in), Options{Lenient: tc.lenient})
			e := checkError(t, []byte(tc.in), err)
			if e.Code != tc.code || e.Pos.Line != tc.line || e.Pos.Column != tc.col || !strings.Contains(e.Message, tc.msg) {
				t.Fatalf("got %s at %d:%d: %s\nwant %s at %d:%d: %s", e.Code, e.Pos.Line, e.Pos.Column, e.Message, tc.code, tc.line, tc.col, tc.msg)
			}
			raw, moved, f := diagnosed([]byte(tc.in), tc.lenient)
			if *moved != *e {
				t.Fatalf("Parse and diagnose disagree: %v, %v", e, moved)
			}
			if moved.Pos.Offset > raw.Pos.Offset {
				t.Errorf("moved past where parsing stopped, %v", raw)
			}
			checkFix(t, []byte(tc.in), tc.lenient, moved, f)
		})
	}
}

// Without anything to go on, the error stays where parsing stopped.
func TestDiagnoseLeavesAlone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        string
		code      Code
		line, col int
	}{
		{"comma on one line", `{"a": 1 "b": 2}`, CodeExpectedCommaOrClose, 1, 9},
		{"last } missing", joinLines(`{`, `  "a": 1,`, `  "b": {`, `    "c": 2`, `  }`, ``), CodeUnexpectedEnd, 6, 1},
		{"minified", `{"a":{"b":1,"c":2}`, CodeUnexpectedEnd, 1, 19},
		{"loose indentation before a mistake on one line", joinLines(`{`, `  "a": {`, `    "b": 1`, `},`, `  "c": 2 "d": 3`, `}`), CodeExpectedCommaOrClose, 5, 10},
		{"a second value at the top", joinLines(`{`, `  "a": 1`, `}`, `{`, `  "b": 2`, `}`), CodeExtraData, 4, 1},
		{"values listed without [ ]", `{"a":1},{"b":2}`, CodeExtraData, 1, 8},
		{"} aligned with its value", joinLines(`{`, `  "a": 1`, `}`, `"b": 2`), CodeExtraData, 4, 1},
		{"tab where the opening line has spaces", "{\n  \"a\": {\n    \"b\": 1,\n\t\"c\": 2\n\t}", CodeUnexpectedEnd, 5, 3},
		{"contents not indented", joinLines(`{`, `"a": {`, `"b": 1,`, `"c": 2`, `}`), CodeUnexpectedEnd, 5, 2},
		{"mismatched on one line", `[1, 2}`, CodeMismatchedBracket, 1, 6},
		{"loose ] with more after it", joinLines(`[`, `  [`, `    1`, `],`, `  []`), CodeUnexpectedEnd, 5, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in), Options{})
			e := checkError(t, []byte(tc.in), err)
			raw, _, f := diagnosed([]byte(tc.in), false)
			if e.Code != tc.code || e.Pos.Line != tc.line || e.Pos.Column != tc.col || e.Message != raw.Message || f != (fix{}) {
				t.Fatalf("got %s at %d:%d: %s (fix %+v), want %s at %d:%d unchanged", e.Code, e.Pos.Line, e.Pos.Column, e.Message, f, tc.code, tc.line, tc.col)
			}
		})
	}
}

// Deleting any comma at the end of a line, or any closing bracket that
// starts one, from pretty-printed JSON must be diagnosed: a comma exactly
// where it was, a bracket with a fix that makes the input valid again.
func TestDiagnoseDeletions(t *testing.T) {
	docs := [][]byte{
		[]byte(`{"site":"dimih.com","tools":[{"path":"/qr","library":"gqr","tags":["png","svg"]},{"path":"/json","library":"jqo","tags":[]}],"owner":{"name":"mihai","city":null,"langs":{"go":true,"bash":true}},"since":2025}`),
		[]byte(`[[1,[2,[3,{"deep":[4,5]}]]],{"a":{"b":{"c":{"d":[]}}}},[{},[],""]]`),
	}
	for _, s := range validSamples {
		docs = append(docs, []byte(s))
	}
	files, _ := filepath.Glob("testdata/format/*.json")
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, b)
	}
	var commas, brackets int
	for _, doc := range docs {
		for _, style := range []Style{Indent2, Indent4, IndentTab} {
			res, err := Format(doc, FormatOptions{Style: style})
			if err != nil {
				t.Fatal(err)
			}
			p := res.Output
			end := len(bytes.TrimRight(p, "\n")) - 1 // the outermost closing bracket
			for i, c := range p {
				switch {
				case c == ',' && i+1 < len(p) && p[i+1] == '\n':
					commas++
					broken := applyFix(p, i, fix{del: 1})
					_, moved, f := diagnosed(broken, false)
					if moved == nil || moved.Code != CodeExpectedCommaOrClose || moved.Pos.Offset != i || f.ins != "," {
						t.Errorf("deleting the comma at %d gave %v (fix %+v) in\n%s", i, moved, f, broken)
						continue
					}
					checkFix(t, broken, false, moved, f)
				case (c == '}' || c == ']') && i != end && len(bytes.TrimLeft(p[bytes.LastIndexByte(p[:i], '\n')+1:i], " \t")) == 0:
					brackets++
					broken := applyFix(p, i, fix{del: 1})
					_, moved, f := diagnosed(broken, false)
					if moved == nil || f == (fix{}) {
						t.Errorf("deleting the %c at %d was not diagnosed (%v) in\n%s", c, i, moved, broken)
						continue
					}
					checkFix(t, broken, false, moved, f)
				}
			}
		}
	}
	if commas < 100 || brackets < 40 {
		t.Fatalf("only %d commas and %d brackets deleted", commas, brackets)
	}
	t.Logf("deleted %d commas and %d brackets, one at a time", commas, brackets)
}

// The diagnosis never panics, never moves an error past where parsing
// stopped, and always leaves a well-formed error.
func FuzzDiagnose(f *testing.F) {
	for _, tc := range diagnoseCases {
		f.Add([]byte(tc.in))
	}
	f.Add([]byte("{\n  \"a\": [\n    {\"b\": [1, 2,\n  ],\n}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, lenient := range []bool{false, true} {
			raw, moved, fx := diagnosed(data, lenient)
			if raw == nil {
				continue
			}
			checkError(t, data, moved)
			if moved.Pos.Offset > raw.Pos.Offset {
				t.Fatalf("moved past where parsing stopped: %v, then %v", raw, moved)
			}
			if moved.Pos.Offset+fx.del > len(data) {
				t.Fatalf("fix %+v at %d runs past the input", fx, moved.Pos.Offset)
			}
			_, _ = Parse(applyFix(data, moved.Pos.Offset, fx), Options{Lenient: lenient})
		}
	})
}
