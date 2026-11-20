package jqo

import (
	"encoding/json"
	"encoding/json/jsontext"
	"strings"
	"testing"
	"unicode/utf8"
)

// validSamples are documents every parser must accept, covering each kind of
// value, escape and number form.
var validSamples = []string{
	`null`, `true`, `false`, `0`, `-0`, `1.5e+10`, `-1E-7`, `""`, `"plain"`,
	`[]`, `{}`, `[[]]`, `{"":{}}`, ` [ 1 , 2 ] `,
	"{\"a\":\t[1,\n2,\r\n3]}",
	`{"esc":"\" \\ \/ \b \f \n \r \t ` + "\\u00e9 \\ud83d\\ude00" + `"}`,
	`{"unicode":"é😀日本"}`,
	`{"n":[0,-0,1,-1,0.5,1e5,1E+5,1e-5,12345678901234567890,-9007199254740993]}`,
	`{"a":1,"a":2}`,
	"[\"\\ud800\"]",
	`{"nested":{"deeper":{"deepest":[{"x":null}]}}}`,
	`[1,"two",true,false,null,{"k":"v"},[3]]`,
}

// hasLoneSurrogate reports whether data, valid JSON, contains a lone
// surrogate escape, the one thing jsontext rejects and Parse accepts.
func hasLoneSurrogate(data []byte) bool {
	s := newScanner(data, false, DefaultMaxDepth)
	s.wantSurrogates = true
	for {
		if k := s.next().kind; k == tokEOF || k == tokError {
			return len(s.surrogates) > 0
		}
	}
}

// checkVerdict compares strict Parse with two independent parsers.
// encoding/json accepts invalid UTF-8 inside strings, which RFC 8259 §8.1
// forbids. jsontext rejects lone surrogate escapes, which are valid syntax.
// Each differs from Parse on exactly one point, and not the same one, so
// every other disagreement is a bug.
func checkVerdict(t testing.TB, data []byte) {
	t.Helper()
	_, err := Parse(data, Options{MaxSize: len(data) + 1})
	ours := err == nil
	if err != nil {
		checkError(t, data, err)
	}
	v1 := json.Valid(data)
	jt := jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true))
	switch {
	case ours && !v1:
		t.Errorf("Parse accepts %q; encoding/json rejects it", data)
	case !ours && v1 && utf8.Valid(data):
		t.Errorf("Parse rejects %q (%v); encoding/json accepts it", data, err)
	case !ours && jt:
		t.Errorf("Parse rejects %q (%v); jsontext accepts it", data, err)
	case ours && !jt && !hasLoneSurrogate(data):
		t.Errorf("Parse accepts %q; jsontext rejects it", data)
	}
}

func TestVerdictMatchesOracles(t *testing.T) {
	var corpus []string
	corpus = append(corpus, validSamples...)
	for _, tc := range errorCases {
		corpus = append(corpus, tc.in)
	}
	for _, tc := range warningCases {
		corpus = append(corpus, tc.in)
	}
	// Every truncation and every one-byte change of the samples.
	for _, s := range validSamples {
		for i := 0; i <= len(s); i++ {
			corpus = append(corpus, s[:i])
		}
		for i := 0; i < len(s); i++ {
			for _, c := range []byte{' ', ',', '"', '\\', '{', ']', '0', 'e', '.', '-', 0, 0x80, 0xff} {
				corpus = append(corpus, s[:i]+string([]byte{c})+s[i+1:])
			}
		}
	}
	for _, s := range corpus {
		checkVerdict(t, []byte(s))
	}
	// The two documented differences, pinned.
	if _, err := Parse([]byte("\"\xff\""), Options{}); err == nil || !json.Valid([]byte("\"\xff\"")) {
		t.Error("invalid UTF-8 difference changed")
	}
	if _, err := Parse([]byte(`"\ud800"`), Options{}); err != nil || jsontext.Value(`"\ud800"`).IsValid() {
		t.Error("lone surrogate difference changed")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range validSamples {
		f.Add([]byte(s))
	}
	for _, tc := range errorCases {
		if len(tc.in) < 1000 {
			f.Add([]byte(tc.in))
		}
	}
	for _, tc := range warningCases {
		f.Add([]byte(tc.in))
	}
	f.Add([]byte("\xef\xbb\xbf{\"a\": [1, 2,], // c\n /* d */ \"b\": 2,}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		checkVerdict(t, data)
		strict, err := Parse(data, Options{})
		lenient, lerr := Parse(data, Options{Lenient: true})
		if lerr != nil {
			checkError(t, data, lerr)
		}
		if err != nil {
			return
		}
		// Lenient mode accepts everything strict mode does, unchanged.
		if lerr != nil || lenient.FixCount != 0 || lenient.Summary != strict.Summary || lenient.WarningCount != strict.WarningCount {
			t.Fatalf("lenient differs on valid %q: %+v %v", data, lenient, lerr)
		}
		for _, w := range strict.Warnings {
			if w.Pos.Offset >= len(data) || w.Pos.Line < 1 || w.Message == "" {
				t.Fatalf("bad warning %+v for %q", w, data)
			}
		}
		if strict.Summary.Size != len(data) {
			t.Fatalf("summary size %d for %d bytes", strict.Summary.Size, len(data))
		}
	})
}

func TestNoQuadraticPositions(t *testing.T) {
	// Many warnings on one long line must not rescan the line per warning.
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 200000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\"\\ud800\"")
	}
	b.WriteString("]")
	rep, err := Parse([]byte(b.String()), Options{})
	if err != nil || rep.WarningCount != 200000 || len(rep.Warnings) != ListLimit {
		t.Fatalf("%v %d", err, rep.WarningCount)
	}
}
