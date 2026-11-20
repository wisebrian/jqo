package jqo

import (
	"bytes"
	"strings"
	"testing"
)

// encodeTimes wraps s in n layers of JSON string encoding.
func encodeTimes(t testing.TB, s string, n int) []byte {
	b := []byte(s)
	for i := 0; i < n; i++ {
		var err error
		if b, err = EncodeString(b, Escape{}); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestDecodeString(t *testing.T) {
	triple := encodeTimes(t, `{"a":[1,"x"]}`, 3)
	for _, tc := range []struct {
		name      string
		in        []byte
		out       string
		layers    int
		maxLayers int
	}{
		{"plain text", []byte(`"plain text"`), "plain text", 1, 0},
		{"surrounding space", []byte("  \"{\\\"a\\\":1}\"\n"), `{"a":1}`, 1, 0},
		{"escapes", []byte("\"tab\\there \\u00e9 \\ud83d\\ude00 \\/\""), "tab\there é 😀 /", 1, 0},
		{"double encoded", []byte("\"\\\"{\\\\\\\"a\\\\\\\":1}\\\"\""), `{"a":1}`, 2, 0},
		{"triple encoded", triple, `{"a":[1,"x"]}`, 3, 0},
		{"layer cap", triple, string(encodeTimes(t, `{"a":[1,"x"]}`, 1)), 2, 2},
		{"stops at JSON text", []byte("\"\\\"hello\\\"\""), `"hello"`, 1, 0},
		{"unwraps a string holding an array", []byte("\"\\\"[1]\\\"\""), `[1]`, 2, 0},
	} {
		out, layers, err := DecodeString(tc.in, tc.maxLayers)
		if err != nil || string(out) != tc.out || layers != tc.layers {
			t.Errorf("%s: DecodeString(%s) = %q, %d, %v; want %q, %d", tc.name, tc.in, out, layers, err, tc.out, tc.layers)
		}
	}
}

func TestDecodeStringErrors(t *testing.T) {
	for _, tc := range []struct {
		in   string
		code Code
		col  int
		msg  string
	}{
		{"", CodeNotAString, 1, "double quotes"},
		{"  ", CodeNotAString, 3, "double quotes"},
		{`{"a":1}`, CodeNotAString, 1, "already JSON"},
		{`'x'`, CodeNotAString, 1, "single quotes"},
		{`"abc`, CodeUnterminatedString, 1, "never closed"},
		{`"a" x`, CodeExtraData, 5, "after the closing quote"},
		{"\"a\\ud800b\"", CodeLoneSurrogate, 3, "can't be decoded"},
		{"\"a\\qb\"", CodeInvalidEscape, 3, "isn't an escape"},
		{"\"line\nbreak\"", CodeControlCharacter, 6, "line break"},
	} {
		_, _, err := DecodeString([]byte(tc.in), 0)
		e := checkError(t, []byte(tc.in), err)
		if e.Code != tc.code || e.Pos.Column != tc.col || !strings.Contains(e.Message, tc.msg) {
			t.Errorf("DecodeString(%q) = %v (%s), want %s at column %d", tc.in, e, e.Code, tc.code, tc.col)
		}
	}
}

func TestDecodeBase64(t *testing.T) {
	binary := []byte{0xfb, 0xff, 0xbf, 0x00, 0x10}
	for _, tc := range []struct {
		in  string
		out []byte
	}{
		{"eyJhIjoxfQ==", []byte(`{"a":1}`)},
		{"eyJhIjoxfQ", []byte(`{"a":1}`)},
		{"eyJh\r\nIjox fQ==\n", []byte(`{"a":1}`)},
		{"+/+/ABA=", binary},
		{"-_-_ABA", binary},
		{"", []byte{}},
		{"  \n", []byte{}},
	} {
		out, err := DecodeBase64([]byte(tc.in))
		if err != nil || !bytes.Equal(out, tc.out) {
			t.Errorf("DecodeBase64(%q) = %q, %v; want %q", tc.in, out, err, tc.out)
		}
	}
	for _, tc := range []struct {
		in  string
		col int
		msg string
	}{
		{"ab$c", 3, "$ is not a base64 character"},
		{"ab-+", 4, "mixes the two base64 alphabets"},
		{"abc=d", 5, "only come at the very end"},
		{"abcde", 5, "one character too many or too few"},
		{"ab===", 3, "padding doesn't fit"},
		{"abc==", 4, "padding doesn't fit"},
		{"ab\xffcd", 3, "not a base64 character"},
	} {
		_, err := DecodeBase64([]byte(tc.in))
		e := checkError(t, []byte(tc.in), err)
		if e.Code != CodeInvalidBase64 || e.Pos.Column != tc.col || !strings.Contains(e.Message, tc.msg) {
			t.Errorf("DecodeBase64(%q) = %v, want column %d and %q", tc.in, e, tc.col, tc.msg)
		}
	}
}

func TestDecodePercent(t *testing.T) {
	for _, tc := range []struct {
		in       string
		keepPlus bool
		out      string
	}{
		{"%7B%22a%22%3A1%7D", false, `{"a":1}`},
		{"a+b%20c", false, "a b c"},
		{"a+b%20c", true, "a+b c"},
		{"%e2%82%AC", false, "€"},
		{"plain", false, "plain"},
	} {
		out, err := DecodePercent([]byte(tc.in), tc.keepPlus)
		if err != nil || string(out) != tc.out {
			t.Errorf("DecodePercent(%q, %v) = %q, %v; want %q", tc.in, tc.keepPlus, out, err, tc.out)
		}
	}
	for in, col := range map[string]int{"%zz": 1, "50%": 3, "a%4": 2} {
		_, err := DecodePercent([]byte(in), false)
		if e := checkError(t, []byte(in), err); e.Code != CodeInvalidPercent || e.Pos.Column != col {
			t.Errorf("DecodePercent(%q) = %v, want column %d", in, e, col)
		}
	}
}

func TestCodecSizeLimit(t *testing.T) {
	big := make([]byte, DefaultMaxSize+1)
	for name, fn := range map[string]func() error{
		"DecodeString":  func() error { _, _, err := DecodeString(big, 0); return err },
		"DecodeBase64":  func() error { _, err := DecodeBase64(big); return err },
		"DecodePercent": func() error { _, err := DecodePercent(big, false); return err },
		"DecodeJWT":     func() error { _, err := DecodeJWT(big, 0); return err },
		"EncodeString":  func() error { _, err := EncodeString(big, Escape{}); return err },
		"EncodeBase64":  func() error { _, err := EncodeBase64(big, Base64Options{}); return err },
		"EncodePercent": func() error { _, err := EncodePercent(big); return err },
		"EncodeJWT":     func() error { _, err := EncodeJWT(big, []byte("{}"), nil); return err },
	} {
		if e := asError(t, fn()); e.Code != CodeTooLarge {
			t.Errorf("%s: %v", name, e)
		}
	}
	if Detect(big) != nil {
		t.Error("Detect looked at oversized input")
	}
}
