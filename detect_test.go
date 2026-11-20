package jqo

import (
	"encoding/base64"
	"testing"
)

func TestDetect(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString
	for _, tc := range []struct {
		name  string
		in    string
		first Encoding
		conf  string
	}{
		{"object", `{"a":1}`, EncJSON, "high"},
		{"number", " 42 ", EncJSON, "high"},
		{"string holding JSON", "\"{\\\"a\\\":1}\"", EncString, "high"},
		{"triple encoded", string(encodeTimes(t, `[1]`, 3)), EncString, "high"},
		{"plain string", `"hello"`, EncJSON, "high"},
		{"lenient JSON", "{\"a\":1, // c\n}", EncJSON, "medium"},
		{"JWT", sampleToken, EncJWT, "high"},
		{"JWT in a header", "Bearer " + sampleToken, EncJWT, "high"},
		{"JWE", "eyJhbGciOiJSU0EtT0FFUCJ9.a.b.c.d", EncJWT, "medium"},
		{"base64 JSON", b64([]byte(`{"a":1}`)), EncBase64, "high"},
		{"base64url JSON", base64.RawURLEncoding.EncodeToString([]byte(`[">>>?"]`)), EncBase64, "high"},
		{"wrapped base64 JSON", "eyJhIjox\nfQ==", EncBase64, "high"},
		{"base64 text", b64([]byte("just some words")), EncBase64, "medium"},
		{"base64 gzip", b64([]byte{0x1f, 0x8b, 8, 0, 0, 0}), EncBase64, "low"},
		{"percent JSON", "%7B%22a%22%3A1%7D", EncPercent, "high"},
		{"URL with JSON", "https://x.test/?q=%7B%22a%22%3A1%7D", EncPercent, "medium"},
	} {
		gs := Detect([]byte(tc.in))
		if len(gs) == 0 || gs[0].Encoding != tc.first || gs[0].Confidence != tc.conf || gs[0].Reason == "" {
			t.Errorf("%s: Detect(%q) = %+v, want %s (%s) first", tc.name, tc.in, gs, tc.first, tc.conf)
		}
	}
	for _, in := range []string{"", "   ", "hello world", "a.b"} {
		if gs := Detect([]byte(in)); len(gs) != 0 {
			t.Errorf("Detect(%q) = %+v, want nothing", in, gs)
		}
	}
	if gs := Detect([]byte(`"hello"`)); len(gs) != 2 || gs[1].Encoding != EncString || gs[1].Confidence != "low" {
		t.Errorf("plain string alternatives: %+v", gs)
	}
}
