package jqo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestEncodeString(t *testing.T) {
	for _, tc := range []struct {
		in   string
		esc  Escape
		want string
	}{
		{"he said \"hi\"\n", Escape{}, "\"he said \\\"hi\\\"\\n\""},
		{"a\\b\tc\x01\x7f", Escape{}, "\"a\\\\b\\tc\\u0001\x7f\""},
		{"\b\f\r", Escape{}, "\"\\b\\f\\r\""},
		{"é😀", Escape{}, "\"é😀\""},
		{"é😀", Escape{NonASCII: true}, "\"\\u00e9\\ud83d\\ude00\""},
		{"<a & b>", Escape{}, "\"<a & b>\""},
		{"<a & b>", Escape{HTML: true}, "\"\\u003ca \\u0026 b\\u003e\""},
		{"line\xe2\x80\xa8sep", Escape{}, "\"line\\u2028sep\""}, // always escaped
		{`{"a":1}`, Escape{}, "\"{\\\"a\\\":1}\""},
		{"", Escape{}, `""`},
	} {
		out, err := EncodeString([]byte(tc.in), tc.esc)
		if err != nil || string(out) != tc.want {
			t.Errorf("EncodeString(%q, %+v) = %s, %v; want %s", tc.in, tc.esc, out, err, tc.want)
		}
		var back string
		if err := json.Unmarshal(out, &back); err != nil || back != tc.in {
			t.Errorf("encoding/json reads %s as %q, %v", out, back, err)
		}
	}
	_, err := EncodeString([]byte("ok\xffno"), Escape{})
	if e := checkError(t, []byte("ok\xffno"), err); e.Code != CodeInvalidUTF8 || e.Pos.Column != 3 {
		t.Errorf("invalid UTF-8: %v", e)
	}
}

func TestEncodeBase64(t *testing.T) {
	data := []byte{0xfb, 0xff, 0xbf, 'a'}
	for opts, enc := range map[Base64Options]*base64.Encoding{
		{}:                           base64.StdEncoding,
		{URL: true}:                  base64.URLEncoding,
		{NoPadding: true}:            base64.RawStdEncoding,
		{URL: true, NoPadding: true}: base64.RawURLEncoding,
	} {
		out, err := EncodeBase64(data, opts)
		if err != nil || string(out) != enc.EncodeToString(data) {
			t.Errorf("%+v: %s, %v", opts, out, err)
		}
	}
}

func TestEncodePercent(t *testing.T) {
	out, err := EncodePercent([]byte(`{"a b":"ü/+~._-"}`))
	if want := "%7B%22a%20b%22%3A%22%C3%BC%2F%2B~._-%22%7D"; err != nil || string(out) != want {
		t.Errorf("got %s, %v; want %s", out, err, want)
	}
}

// Every encoder must round-trip through its decoder.
func TestRoundTrips(t *testing.T) {
	samples := [][]byte{nil, []byte("x"), []byte(`{"a":[1,"é",null]}`), []byte("a+b c%d\n\t\"\\"), []byte("😀\u2028<&>"), bytes.Repeat([]byte{0, 1, 0xfe, 0xff}, 50)}
	for _, s := range samples {
		checkRoundTrips(t, s)
	}
}

func checkRoundTrips(t testing.TB, s []byte) {
	t.Helper()
	for _, esc := range []Escape{{}, {NonASCII: true}, {HTML: true}, {true, true}} {
		enc, err := EncodeString(s, esc)
		if err != nil {
			if asError(t, err).Code != CodeInvalidUTF8 {
				t.Fatalf("EncodeString(%q): %v", s, err)
			}
			continue
		}
		dec, layers, err := DecodeString(enc, 1)
		if err != nil || !bytes.Equal(dec, s) || layers != 1 {
			t.Fatalf("DecodeString(EncodeString(%q, %+v)) = %q, %d, %v", s, esc, dec, layers, err)
		}
	}
	for _, opts := range []Base64Options{{}, {URL: true}, {NoPadding: true}, {URL: true, NoPadding: true}} {
		enc, err := EncodeBase64(s, opts)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := DecodeBase64(enc)
		if err != nil || !bytes.Equal(dec, s) {
			t.Fatalf("DecodeBase64(EncodeBase64(%q, %+v)) = %q, %v", s, opts, dec, err)
		}
	}
	enc, err := EncodePercent(s)
	if err != nil {
		t.Fatal(err)
	}
	if dec, err := DecodePercent(enc, false); err != nil || !bytes.Equal(dec, s) {
		t.Fatalf("DecodePercent(EncodePercent(%q)) = %q, %v", s, dec, err)
	}
}
