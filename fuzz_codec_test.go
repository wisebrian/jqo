package jqo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"hash"
	"testing"
	"unicode/utf8"
)

// The decoders must never fail with anything but a positioned *Error, and
// what they decode must survive the matching encoder and come back.

func FuzzDecodeString(f *testing.F) {
	f.Add([]byte("\"\\\"{\\\\\\\"a\\\\\\\":1}\\\"\""), uint8(0))
	f.Add([]byte("\"tab\\t \\u00e9 \\ud83d\\ude00\""), uint8(1))
	f.Add([]byte("\"\\ud800\""), uint8(3))
	f.Fuzz(func(t *testing.T, data []byte, maxLayers uint8) {
		out, layers, err := DecodeString(data, int(maxLayers))
		if err != nil {
			checkError(t, data, err)
			return
		}
		limit := int(maxLayers)
		if limit == 0 {
			limit = DefaultMaxLayers
		}
		if layers < 1 || layers > limit || !utf8.Valid(out) {
			t.Fatalf("DecodeString(%q) = %q, %d layers", data, out, layers)
		}
		checkRoundTrips(t, out)
	})
}

func FuzzDecodeBase64(f *testing.F) {
	f.Add([]byte("eyJhIjoxfQ=="))
	f.Add([]byte("-_-_\r\nABA"))
	f.Add([]byte("ab=c"))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := DecodeBase64(data)
		if err != nil {
			checkError(t, data, err)
			return
		}
		checkRoundTrips(t, out)
		// Without whitespace or padding, standard base64 decoders agree.
		if !bytes.ContainsAny(data, " \t\r\n\f\v=-_") {
			if want, err := base64.RawStdEncoding.DecodeString(string(data)); err == nil && !bytes.Equal(out, want) {
				t.Fatalf("DecodeBase64(%q) = %q, encoding/base64 says %q", data, out, want)
			}
		}
	})
}

func FuzzDecodePercent(f *testing.F) {
	f.Add([]byte("%7B%22a%22%3A1%7D"), false)
	f.Add([]byte("a+b%2"), true)
	f.Fuzz(func(t *testing.T, data []byte, keepPlus bool) {
		out, err := DecodePercent(data, keepPlus)
		if err != nil {
			checkError(t, data, err)
			return
		}
		checkRoundTrips(t, out)
	})
}

func FuzzDecodeJWT(f *testing.F) {
	f.Add([]byte(sampleToken), int64(1700000000))
	f.Add([]byte("Bearer "+sampleToken), int64(0))
	f.Add(unsignedToken(`{"exp":1.5e9,"nbf":"x","iat":-1}`), int64(-5))
	f.Add([]byte("a.b.c.d.e"), int64(0))
	f.Fuzz(func(t *testing.T, data []byte, now int64) {
		j, err := DecodeJWT(data, now)
		if err != nil {
			checkError(t, data, err)
			return
		}
		if j.SignatureChecked || len(j.Times) > 3 {
			t.Fatalf("got %+v", j)
		}
		for _, e := range []*Error{j.HeaderError, j.PayloadError} {
			if e != nil && (e.Code == CodeInternal || e.Pos.Line == 0 && e.Code != CodeTooLarge) {
				t.Fatalf("bad part error %v", e)
			}
		}
		for _, c := range j.Times {
			if (c.Problem == "") == (c.UTC == "") {
				t.Fatalf("claim %+v has both or neither of a date and a problem", c)
			}
		}
		if b, _ := j.MarshalJSON(); !json.Valid(b) {
			t.Fatalf("invalid JSON %s", b)
		}
	})
}

func FuzzDetect(f *testing.F) {
	for _, s := range []string{`{"a":1}`, sampleToken, "eyJhIjoxfQ==", "%7B%7D", "\"\\\"[1]\\\"\"", "a.b.c.d.e"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, g := range Detect(data) {
			if g.Confidence != "high" && g.Confidence != "medium" && g.Confidence != "low" || g.Reason == "" {
				t.Fatalf("Detect(%q): %+v", data, g)
			}
		}
	})
}

func FuzzEncodeString(f *testing.F) {
	f.Add([]byte("he said \"hi\"\n"), false, false)
	f.Add([]byte("é😀<&>\x01\x7f"), true, true)
	f.Fuzz(func(t *testing.T, text []byte, nonASCII, html bool) {
		out, err := EncodeString(text, Escape{NonASCII: nonASCII, HTML: html})
		if !utf8.Valid(text) {
			if err == nil {
				t.Fatalf("encoded invalid UTF-8 %q", text)
			}
			checkError(t, text, err)
			return
		}
		var back string
		if err != nil || json.Unmarshal(out, &back) != nil || back != string(text) {
			t.Fatalf("EncodeString(%q) = %s, %v; encoding/json reads %q", text, out, err, back)
		}
		if nonASCII && bytes.IndexFunc(out, func(r rune) bool { return r >= utf8.RuneSelf }) >= 0 {
			t.Fatalf("non-ASCII left in %s", out)
		}
		if html && bytes.ContainsAny(out, "<>&") {
			t.Fatalf("HTML characters left in %s", out)
		}
		checkRoundTrips(t, text)
	})
}

func FuzzEncodeBase64(f *testing.F) {
	f.Add([]byte{0xfb, 0xff}, false, false)
	f.Fuzz(func(t *testing.T, data []byte, url, noPadding bool) {
		out, err := EncodeBase64(data, Base64Options{URL: url, NoPadding: noPadding})
		if err != nil {
			t.Fatal(err)
		}
		if dec, err := DecodeBase64(out); err != nil || !bytes.Equal(dec, data) {
			t.Fatalf("round trip of %q: %q, %v", data, dec, err)
		}
	})
}

func FuzzEncodePercent(f *testing.F) {
	f.Add([]byte("a b+c%"))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := EncodePercent(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range out {
			if !isLetter(c) && !isDigit(c) && !bytes.ContainsRune([]byte("-._~%"), rune(c)) {
				t.Fatalf("unsafe %q in %s", c, out)
			}
		}
		if dec, err := DecodePercent(out, false); err != nil || !bytes.Equal(dec, data) {
			t.Fatalf("round trip of %q: %q, %v", data, dec, err)
		}
	})
}

func FuzzEncodeJWT(f *testing.F) {
	f.Add([]byte(samplePayload), []byte(sampleSecret), uint8(0))
	f.Add([]byte(`{"exp":1e3,"x":[1,{"é":null}]}`), []byte{}, uint8(3))
	f.Fuzz(func(t *testing.T, payload, secret []byte, alg uint8) {
		name := [...]string{"HS256", "HS384", "HS512", "none"}[alg%4]
		hashes := map[string]func() hash.Hash{"HS256": sha256.New, "HS384": sha512.New384, "HS512": sha512.New}
		tok, err := EncodeJWT([]byte(`{"alg":"`+name+`","typ":"JWT"}`), payload, secret)
		if err != nil {
			checkError(t, payload, err)
			return
		}
		j, err := DecodeJWT(tok, 0)
		if err != nil || j.Alg != name || j.PayloadError != nil {
			t.Fatalf("DecodeJWT(EncodeJWT(%q)) = %+v, %v", payload, j, err)
		}
		min, _ := Format(payload, FormatOptions{Style: Minify})
		if !bytes.Equal(j.Payload, min.Output) {
			t.Fatalf("payload %s, want %s", j.Payload, min.Output)
		}
		dot := bytes.LastIndexByte(tok, '.')
		if name == "none" {
			if dot != len(tok)-1 {
				t.Fatalf("unsigned token %s has a signature", tok)
			}
			return
		}
		m := hmac.New(hashes[name], secret)
		m.Write(tok[:dot])
		if got := string(tok[dot+1:]); got != base64.RawURLEncoding.EncodeToString(m.Sum(nil)) {
			t.Fatalf("%s signature differs from crypto/hmac", name)
		}
	})
}
