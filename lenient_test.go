package jqo

import (
	"bytes"
	"testing"
)

func TestRepair(t *testing.T) {
	for _, tc := range []struct {
		name, in, out string
		fixes         []Code
	}{
		{"typical config",
			"{\n  // comment\n  \"a\": 1, /* inline */\n  \"b\": [1, 2,],\n}\n",
			"{\n  \"a\": 1,\n  \"b\": [1, 2]\n}\n",
			[]Code{CodeComment, CodeComment, CodeTrailingComma, CodeTrailingComma}},
		{"byte order mark", "\xef\xbb\xbf[1]", "[1]", []Code{CodeBOM}},
		{"comment between comma and bracket", "[1, /* c */ ]", "[1 ]", []Code{CodeTrailingComma, CodeComment}},
		{"comment at the end", "[1] // end", "[1]", []Code{CodeComment}},
		{"comment at the end with newline", "[1]\n// end\n", "[1]\n", []Code{CodeComment}},
		{"block comment over lines", "[\n  /* x\n  y */\n  1\n]", "[\n  1\n]", []Code{CodeComment}},
		{"crlf", "[1,\r\n// c\r\n2]", "[1,\r\n2]", []Code{CodeComment}},
		{"two comments on a line", "[1, /* a */ /* b */ 2]", "[1, 2]", []Code{CodeComment, CodeComment}},
		{"comment touching a value", "{\"a\":1/*x*/}", "{\"a\":1}", []Code{CodeComment}},
		{"nothing to do", "{\"a\": [1, 2]}\n", "{\"a\": [1, 2]}\n", nil},
	} {
		res, err := Repair([]byte(tc.in), Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(res.Output) != tc.out {
			t.Errorf("%s: got %q, want %q", tc.name, res.Output, tc.out)
		}
		if res.FixCount != len(tc.fixes) || len(res.Fixes) != len(tc.fixes) {
			t.Fatalf("%s: fixes %+v", tc.name, res.Fixes)
		}
		for i, code := range tc.fixes {
			if res.Fixes[i].Code != code {
				t.Errorf("%s: fix %d is %s, want %s", tc.name, i, res.Fixes[i].Code, code)
			}
			if i > 0 && res.Fixes[i].Pos.Offset < res.Fixes[i-1].Pos.Offset {
				t.Errorf("%s: fixes out of order", tc.name)
			}
		}
		if _, err := Parse(res.Output, Options{}); err != nil {
			t.Errorf("%s: output is not strict JSON: %v", tc.name, err)
		}
	}
	res, _ := Repair([]byte("[\n  1, // one\n  2\n]"), Options{})
	f := res.Fixes[0]
	if f.Pos.Line != 2 || f.Pos.Column != 6 || f.End.Column != 12 || f.Message != "removed a // comment" {
		t.Errorf("comment position: %+v", f)
	}
	for _, in := range []string{"{'a': 1}", "[1,,]", "/* open", ""} {
		_, err := Repair([]byte(in), Options{})
		_, perr := Parse([]byte(in), Options{Lenient: true})
		if a, b := asError(t, err), asError(t, perr); a.Code != b.Code || a.Pos != b.Pos {
			t.Errorf("Repair(%q) error %v, lenient Parse %v", in, a, b)
		}
	}
}

func checkRepair(t testing.TB, data []byte) {
	t.Helper()
	res, err := Repair(data, Options{})
	rep, perr := Parse(data, Options{Lenient: true})
	if (err == nil) != (perr == nil) {
		t.Fatalf("Repair and lenient Parse disagree on %q: %v, %v", data, err, perr)
	}
	if err != nil {
		checkError(t, data, err)
		return
	}
	if res.FixCount != rep.FixCount {
		t.Fatalf("%d fixes, Parse counted %d", res.FixCount, rep.FixCount)
	}
	if _, err := Parse(res.Output, Options{MaxSize: len(data) + 1}); err != nil {
		t.Fatalf("Repair(%q) = %q, not strict JSON: %v", data, res.Output, err)
	}
	// Repairing changes the layout, never the content.
	a, err1 := Format(data, FormatOptions{Options: Options{Lenient: true}, Style: Minify})
	b, err2 := Format(res.Output, FormatOptions{Style: Minify})
	if err1 != nil || err2 != nil || !bytes.Equal(a.Output, b.Output) {
		t.Fatalf("content changed: %q -> %q (%v, %v)", data, res.Output, err1, err2)
	}
	again, err := Repair(res.Output, Options{})
	if err != nil || again.FixCount != 0 || !bytes.Equal(again.Output, res.Output) {
		t.Fatalf("repairing %q again changed it", res.Output)
	}
}

func FuzzRepair(f *testing.F) {
	f.Add([]byte("\xef\xbb\xbf{\n  // c\n  \"a\": [1, /* x */ 2,],\n}"))
	f.Add([]byte("[1, /* c */ ]"))
	f.Add([]byte("[1]\r\n// end"))
	f.Fuzz(func(t *testing.T, data []byte) { checkRepair(t, data) })
}
