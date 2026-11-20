package jqo

import (
	"strconv"
	"strings"
	"testing"
)

// errorCases lists every rejection the scanner can make, with where it must
// point. The columns are counted by hand, in characters.
var errorCases = []struct {
	name      string
	in        string
	code      Code
	line, col int
	msg       string // a fragment the message must contain
}{
	{"empty", "", CodeEmptyInput, 1, 1, "empty"},
	{"only whitespace", "  \n\t ", CodeEmptyInput, 1, 1, "only spaces"},
	{"bom", "\xef\xbb\xbf{}", CodeBOM, 1, 1, "byte order mark"},
	{"utf-16 bom", "\xff\xfe{\x00}\x00", CodeInvalidUTF8, 1, 1, "UTF-16"},
	{"utf-16 without bom", "{\x00}\x00", CodeInvalidUTF8, 1, 1, "UTF-16"},
	{"invalid utf-8 in string", "{\"a\": \"caf\xe9\"}", CodeInvalidUTF8, 1, 11, "UTF-8"},
	{"invalid utf-8 outside", "[\xff]", CodeInvalidUTF8, 1, 2, "UTF-8"},
	{"line comment", "{\n  // note\n  \"a\": 1\n}", CodeComment, 2, 3, "comments"},
	{"block comment", "[1, /* x */ 2]", CodeComment, 1, 5, "comments"},
	{"comment after value", "{} // done", CodeComment, 1, 4, "comments"},
	{"comment instead of colon", "{\"a\" /* x */ : 1}", CodeComment, 1, 6, "comments"},
	{"trailing comma in array", "[1, 2,]", CodeTrailingComma, 1, 6, "after the last item"},
	{"trailing comma in object", "{\"a\": 1,\n}", CodeTrailingComma, 1, 8, "after the last item"},
	{"single-quoted value", "{\"a\": 'x'}", CodeSingleQuote, 1, 7, "double quotes"},
	{"single-quoted key", "{'a': 1}", CodeSingleQuote, 1, 2, "double quotes"},
	{"single-quoted element", "['a']", CodeSingleQuote, 1, 2, "double quotes"},
	{"smart quote key", "{“a”: 1}", CodeSmartQuote, 1, 2, "curly quote"},
	{"smart quote value", "{\"a\": “x”}", CodeSmartQuote, 1, 7, "curly quote"},
	{"unquoted key", "{a: 1}", CodeUnquotedKey, 1, 2, `write "a"`},
	{"unquoted numeric key", "{1: 2}", CodeUnquotedKey, 1, 2, `write "1"`},
	{"unquoted key after comma", "{\"a\": 1, b_2 : 2}", CodeUnquotedKey, 1, 10, `write "b_2"`},
	{"NaN", "[NaN]", CodeNaNInfinity, 1, 2, "NaN"},
	{"Infinity", "[1, Infinity]", CodeNaNInfinity, 1, 5, "Infinity"},
	{"-Infinity", "[-Infinity]", CodeNaNInfinity, 1, 2, "-Infinity"},
	{"leading zero", "[01]", CodeLeadingZero, 1, 2, "write 1, not 01"},
	{"negative leading zeros", "[-007]", CodeLeadingZero, 1, 3, "write 7, not 007"},
	{"all zeros", "[00]", CodeLeadingZero, 1, 2, "write 0, not 00"},
	{"hexadecimal", "[0x1F]", CodeInvalidNumber, 1, 2, "hexadecimal"},
	{"leading point", "[.5]", CodeInvalidNumber, 1, 2, "0.5"},
	{"plus sign", "[+1]", CodeInvalidNumber, 1, 2, "+"},
	{"trailing point", "[1.]", CodeInvalidNumber, 1, 3, "write 1.0 or 1"},
	{"bare exponent", "[1e]", CodeInvalidNumber, 1, 3, "exponent"},
	{"exponent sign only", "[1e+]", CodeInvalidNumber, 1, 3, "exponent"},
	{"minus alone", "[-]", CodeInvalidNumber, 1, 2, "minus"},
	{"double minus", "--1", CodeInvalidNumber, 1, 1, "minus"},
	{"two points", "1.2.3", CodeInvalidNumber, 1, 1, "1.2.3 is not a valid number"},
	{"units", "[12px]", CodeInvalidNumber, 1, 2, "12px"},
	{"True", "[True]", CodeInvalidLiteral, 1, 2, "write true in lowercase"},
	{"NULL", "NULL", CodeInvalidLiteral, 1, 1, "write null in lowercase"},
	{"undefined", "{\"a\": undefined}", CodeInvalidLiteral, 1, 7, "undefined"},
	{"None", "[None]", CodeInvalidLiteral, 1, 2, "Python"},
	{"incomplete true", "[tru]", CodeInvalidLiteral, 1, 2, "did you mean true"},
	{"incomplete null", "nul", CodeInvalidLiteral, 1, 1, "did you mean null"},
	{"hex escape", "\"a\\x41\"", CodeInvalidEscape, 1, 3, `\u0041`},
	{"quote escape", "\"it\\'s\"", CodeInvalidEscape, 1, 4, "single quote"},
	{"unknown escape", "\"\\q\"", CodeInvalidEscape, 1, 2, "isn't an escape"},
	{"short unicode escape", "\"\\u00\"", CodeInvalidEscape, 1, 2, "four hexadecimal digits"},
	{"bad hex in unicode escape", "\"\\u12G4\"", CodeInvalidEscape, 1, 2, "four hexadecimal digits"},
	{"uppercase U escape", "\"\\U0041\"", CodeInvalidEscape, 1, 2, "lowercase"},
	{"raw tab in string", "\"a\tb\"", CodeControlCharacter, 1, 3, `\t`},
	{"raw line break in string", "{\"a\": \"line\nnext\"}", CodeControlCharacter, 1, 12, `\n`},
	{"raw control character", "\"\x01\"", CodeControlCharacter, 1, 2, `\u0001`},
	{"unterminated string", "{\"a\": \"abc", CodeUnterminatedString, 1, 7, "never closed"},
	{"backslash at end", "\"abc\\", CodeUnterminatedString, 1, 1, "never closed"},
	{"no-break space", "{\"a\":\u00a01}", CodeUnicodeSpace, 1, 6, "no-break space"},
	{"zero-width space", "[1,\u200b2]", CodeUnicodeSpace, 1, 4, "zero-width space"},
	{"trailing no-break space", "{}\u00a0", CodeUnicodeSpace, 1, 3, "no-break space"},
	{"missing value", "{\"a\": }", CodeExpectedValue, 1, 7, "after the colon"},
	{"unquoted text value", "{\"a\": hello world}", CodeExpectedValue, 1, 7, `write "hello world"`},
	{"two commas in array", "[1,,2]", CodeExpectedValue, 1, 4, "two commas"},
	{"leading comma", "[,1]", CodeExpectedValue, 1, 2, "expected a value or ]"},
	{"close at top", "]", CodeExpectedValue, 1, 1, "expected a value"},
	{"key not a string", "{[1]: 2}", CodeExpectedKey, 1, 2, "expected a key"},
	{"two commas in object", "{\"a\":1,,}", CodeExpectedKey, 1, 8, "two commas"},
	{"missing colon", "{\"a\" 1}", CodeExpectedColon, 1, 6, `after the key "a"`},
	{"key without value", "{\"a\"}", CodeExpectedColon, 1, 5, "expected :"},
	{"equals instead of colon", "{\"a\" = 1}", CodeExpectedColon, 1, 6, "found ="},
	{"missing comma in object", "{\n  \"a\": 1\n  \"b\": 2\n}", CodeExpectedCommaOrClose, 2, 9, "a comma is missing here, before the key at line 3, column 3"},
	{"missing comma in array", "[1 2]", CodeExpectedCommaOrClose, 1, 4, "expected , or ] after the value"},
	{"stray character after value", "[1 :]", CodeExpectedCommaOrClose, 1, 4, "found :"},
	{"mismatched close", "[1, 2}", CodeMismatchedBracket, 1, 6, "close the array that starts at line 1, column 1"},
	{"mismatched object close", "{\"a\":1]", CodeMismatchedBracket, 1, 7, "close the object"},
	{"mismatched empty", "{]", CodeMismatchedBracket, 1, 2, "found ]"},
	{"unexpected end", "{\"a\": [1, 2", CodeUnexpectedEnd, 1, 12, "array that starts at line 1, column 7"},
	{"unexpected end after key", "{\"a\"", CodeUnexpectedEnd, 1, 5, "object"},
	{"unexpected end on second line", "[\n  1,", CodeUnexpectedEnd, 2, 5, "closed with ]"},
	{"second value", "{} {}", CodeExtraData, 1, 4, "second value"},
	{"json lines", "{\"a\":1}\n{\"b\":2}", CodeExtraData, 2, 1, "JSON Lines"},
	{"comma after value", "{\"a\":1},{\"b\":2}", CodeExtraData, 1, 8, "inside [ and ]"},
	{"extra close", "[1]]", CodeExtraData, 1, 4, "nothing to close"},
	{"too deep", strings.Repeat("[", DefaultMaxDepth+1), CodeTooDeep, 1, DefaultMaxDepth + 1, "10000 levels"},
}

func TestParseErrors(t *testing.T) {
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := Parse([]byte(tc.in), Options{})
			if err == nil {
				t.Fatalf("Parse(%q) succeeded: %+v", tc.in, rep)
			}
			e := checkError(t, []byte(tc.in), err)
			if e.Code != tc.code || e.Pos.Line != tc.line || e.Pos.Column != tc.col {
				t.Fatalf("Parse(%q) = %s at %d:%d (%v), want %s at %d:%d", tc.in, e.Code, e.Pos.Line, e.Pos.Column, e, tc.code, tc.line, tc.col)
			}
			if !strings.Contains(e.Message, tc.msg) {
				t.Errorf("message %q lacks %q", e.Message, tc.msg)
			}
			want := "Line " + strconv.Itoa(tc.line) + ", column " + strconv.Itoa(tc.col) + ": "
			if !strings.HasPrefix(e.Error(), want) {
				t.Errorf("Error() = %q, want prefix %q", e.Error(), want)
			}
		})
	}
}

func TestParseSpecExample(t *testing.T) {
	_, err := Parse([]byte("{\n  \"a\": 1,\n  \"bb\": 2 \"c\": 3\n}"), Options{})
	const want = "Line 3, column 11: expected , or } after the value (is a comma missing?)"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	e := asError(t, err)
	if e.Snippet != `  "bb": 2 "c": 3` || e.Caret != "          ^" {
		t.Errorf("snippet %q caret %q", e.Snippet, e.Caret)
	}
}

func TestParseLimits(t *testing.T) {
	_, err := Parse([]byte("[1, 2, 3]"), Options{MaxSize: 5})
	e := checkError(t, nil, err)
	if e.Code != CodeTooLarge || e.Pos.Line != 0 || e.Error() != "The input is 9 bytes; the limit is 5 bytes" {
		t.Errorf("got %#v (%v)", e, e)
	}
	if _, err := Parse([]byte("[[[]]]"), Options{MaxDepth: 3}); err != nil {
		t.Errorf("depth 3 within MaxDepth 3: %v", err)
	}
	_, err = Parse([]byte("[[[[]]]]"), Options{MaxDepth: 3})
	if e := checkError(t, []byte("[[[[]]]]"), err); e.Code != CodeTooDeep || e.Pos.Column != 4 {
		t.Errorf("got %v", e)
	}
	// The default depth is exactly encoding/json's: 10000 levels pass.
	deep := strings.Repeat("[", DefaultMaxDepth) + strings.Repeat("]", DefaultMaxDepth)
	if _, err := Parse([]byte(deep), Options{}); err != nil {
		t.Errorf("depth %d: %v", DefaultMaxDepth, err)
	}
}

var warningCases = []struct {
	name  string
	in    string
	code  Code
	line  int
	col   int
	first int // column of the first occurrence, for duplicate keys
	msg   string
}{
	{"duplicate key", `{"a": 1, "a": 2}`, CodeDuplicateKey, 1, 10, 2, `the key "a" appears more than once`},
	{"duplicate after escape", `{"a":1,"\u0061":2}`, CodeDuplicateKey, 1, 8, 2, `"\u0061"`},
	{"duplicate in nested object", "{\"x\": {\"k\": 1,\n \"k\": 2}}", CodeDuplicateKey, 2, 2, 8, "line 1, column 8"},
	{"unsafe integer", `[9007199254740993]`, CodeUnsafeInteger, 1, 2, 0, "read it as 9007199254740992"},
	{"unsafe negative", `[-12345678901234567890]`, CodeUnsafeInteger, 1, 2, 0, "-12345678901234567000"},
	{"unsafe but exact", `[18014398509481984]`, CodeUnsafeInteger, 1, 2, 0, "this one survives"},
	{"unsafe huge", `[` + strings.Repeat("9", 400) + `]`, CodeUnsafeInteger, 1, 2, 0, "Infinity"},
	{"unsafe exponent form", `[123456789012345678901234567890]`, CodeUnsafeInteger, 1, 2, 0, "1.2345678901234568e+29"},
	{"lone high surrogate", `["\ud800"]`, CodeLoneSurrogate, 1, 3, 0, `\ud800 is half of a surrogate pair`},
	{"lone low surrogate", `["x\udc00"]`, CodeLoneSurrogate, 1, 4, 0, `\udc00`},
	{"high before high", `["\ud83d\ud83d\ude00"]`, CodeLoneSurrogate, 1, 3, 0, `\ud83d`},
}

func TestParseWarnings(t *testing.T) {
	for _, tc := range warningCases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := Parse([]byte(tc.in), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if rep.WarningCount != 1 || len(rep.Warnings) != 1 {
				t.Fatalf("got %d warnings: %+v", rep.WarningCount, rep.Warnings)
			}
			w := rep.Warnings[0]
			if w.Code != tc.code || w.Pos.Line != tc.line || w.Pos.Column != tc.col || w.First.Column != tc.first {
				t.Fatalf("got %+v", w)
			}
			if !strings.Contains(w.Message, tc.msg) {
				t.Errorf("message %q lacks %q", w.Message, tc.msg)
			}
		})
	}
}

func TestParseNoWarnings(t *testing.T) {
	for _, in := range []string{
		`[9007199254740992, -9007199254740992, 12345678901234567890.0, 1e300]`,
		`["\ud83d\ude00", "\u00e9", "plain"]`,
		`[{"a": 1}, {"a": 2}]`,
		`{"a": {"a": {"a": 1}}}`,
	} {
		rep, err := Parse([]byte(in), Options{})
		if err != nil || rep.WarningCount != 0 {
			t.Errorf("Parse(%s) = %+v, %v; want no warnings", in, rep, err)
		}
	}
}

func TestParseWarningLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"k":0`)
	for i := 0; i < ListLimit+500; i++ {
		b.WriteString(`,"k":0`)
	}
	b.WriteString("}")
	rep, err := Parse([]byte(b.String()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.WarningCount != ListLimit+500 || len(rep.Warnings) != ListLimit {
		t.Fatalf("count %d, listed %d", rep.WarningCount, len(rep.Warnings))
	}
	if rep.Warnings[0].Pos.Column != 8 || rep.Warnings[ListLimit-1].Pos.Column != 2+6*ListLimit {
		t.Errorf("warnings out of order: first %+v, last %+v", rep.Warnings[0].Pos, rep.Warnings[ListLimit-1].Pos)
	}
}

func TestParseSummary(t *testing.T) {
	in := `{"a": [1, "x", true, null, {"b": false}], "c": "y"}`
	rep, err := Parse([]byte(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := Summary{Size: len(in), MaxDepth: 3, Objects: 2, Arrays: 1, Strings: 2, Numbers: 1, Booleans: 2, Nulls: 1, Keys: 3}
	if rep.Summary != want {
		t.Errorf("got %+v\nwant %+v", rep.Summary, want)
	}
	rep, err = Parse([]byte(` 42 `), Options{})
	if err != nil || rep.Summary != (Summary{Size: 4, Numbers: 1}) {
		t.Errorf("scalar: %+v, %v", rep, err)
	}
}

func TestParseLenient(t *testing.T) {
	in := "\xef\xbb\xbf{\n  // a comment\n  \"a\": [1, 2,], /* b */\n  \"c\": 3,\n}"
	if _, err := Parse([]byte(in), Options{}); asError(t, err).Code != CodeBOM {
		t.Fatalf("strict: %v", err)
	}
	rep, err := Parse([]byte(in), Options{Lenient: true})
	if err != nil {
		t.Fatal(err)
	}
	type fix struct {
		code      Code
		line, col int
	}
	want := []fix{{CodeBOM, 1, 1}, {CodeComment, 2, 3}, {CodeTrailingComma, 3, 13}, {CodeComment, 3, 17}, {CodeTrailingComma, 4, 9}}
	if rep.FixCount != len(want) || len(rep.Fixes) != len(want) {
		t.Fatalf("fixes: %+v", rep.Fixes)
	}
	for i, w := range want {
		f := rep.Fixes[i]
		if f.Code != w.code || f.Pos.Line != w.line || f.Pos.Column != w.col || f.End.Offset <= f.Pos.Offset {
			t.Errorf("fix %d = %+v, want %+v", i, f, w)
		}
	}
	if rep.Fixes[1].Message != "removed a // comment" || rep.Fixes[1].End.Column != 15 {
		t.Errorf("comment fix: %+v", rep.Fixes[1])
	}
	// Lenient mode relaxes only those three things.
	for _, in := range []string{`{'a': 1}`, `{a: 1}`, `[NaN]`, `[01]`, `[1,,2]`, `[,]`, `/* open`} {
		if _, err := Parse([]byte(in), Options{Lenient: true}); err == nil {
			t.Errorf("lenient Parse(%q) succeeded", in)
		}
	}
	_, err = Parse([]byte("[1, /* open"), Options{Lenient: true})
	if e := checkError(t, []byte("[1, /* open"), err); e.Code != CodeUnclosedComment || e.Pos.Column != 5 {
		t.Errorf("unclosed comment: %v", e)
	}
	_, err = Parse([]byte("// nothing here\n"), Options{Lenient: true})
	if e := asError(t, err); e.Code != CodeEmptyInput || !strings.Contains(e.Message, "only comments") {
		t.Errorf("comment-only input: %v", e)
	}
}

func TestGuard(t *testing.T) {
	rethrow = false
	defer func() { rethrow = true }()
	f := func() (n *int, err error) {
		defer guard(&n, &err)
		var m map[string]int
		m["x"] = 1 // panics
		return new(int), nil
	}
	n, err := f()
	if n != nil || asError(t, err).Code != CodeInternal {
		t.Fatalf("got %v, %v", n, err)
	}
}

func TestJSNumber(t *testing.T) {
	for lit, want := range map[string]string{
		"9007199254740993":                "9007199254740992",
		"12345678901234567890":            "12345678901234567000",
		"123456789012345678901":           "123456789012345680000",
		"1234567890123456789012":          "1.2345678901234568e+21",
		"-12345678901234567890":           "-12345678901234567000",
		"0":                               "0",
		"0.000001":                        "0.000001",
		"0.0000001":                       "1e-7",
		"1.5":                             "1.5",
		"1e400":                           "Infinity",
		"-1e400":                          "-Infinity",
		strings.Repeat("9", 309):          "Infinity",
		"100000000000000000000000000000.": "1e+29",
	} {
		if got := jsNumber(lit); got != want {
			t.Errorf("jsNumber(%s) = %s, want %s", lit, got, want)
		}
	}
}
