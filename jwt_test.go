package jqo

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"hash"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// The example token on jwt.io, signed with HS256 and "your-256-bit-secret".
const (
	sampleHeader  = `{"alg":"HS256","typ":"JWT"}`
	samplePayload = `{"sub":"1234567890","name":"John Doe","iat":1516239022}`
	sampleToken   = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	sampleSecret  = "your-256-bit-secret"
)

func TestDecodeJWT(t *testing.T) {
	for _, in := range []string{sampleToken, "  Bearer " + sampleToken + "\n", "bearer\t" + sampleToken} {
		j, err := DecodeJWT([]byte(in), 1700000000)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(j.Header) != sampleHeader || string(j.Payload) != samplePayload || j.Alg != "HS256" ||
			j.SignatureLen != 32 || j.SignatureChecked || j.Expired || j.HeaderError != nil || j.PayloadError != nil {
			t.Errorf("got %+v", j)
		}
		if len(j.Times) != 1 || j.Times[0] != (TimeClaim{Name: "iat", Value: "1516239022", Unix: 1516239022, UTC: "2018-01-18T01:30:22Z", Past: true}) {
			t.Errorf("times %+v", j.Times)
		}
	}
}

// unsignedToken builds an unsigned token around a payload, for claim tests.
func unsignedToken(payload string) []byte {
	enc := base64.RawURLEncoding
	return []byte(enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString([]byte(payload)) + ".")
}

func TestJWTTimes(t *testing.T) {
	const exp = 1700000000 // 2023-11-14T22:13:20Z
	for _, tc := range []struct {
		payload            string
		now                int64
		expired, notYet    bool
		utc, problem, name string
	}{
		{`{"exp":1700000000}`, exp - 1, false, false, "2023-11-14T22:13:20Z", "", "exp"},
		{`{"exp":1700000000}`, exp, true, false, "2023-11-14T22:13:20Z", "", "exp"},
		{`{"exp":1700000000.5}`, exp, false, false, "2023-11-14T22:13:20.5Z", "", "exp"},
		{`{"exp":1.7e9}`, exp + 1, true, false, "2023-11-14T22:13:20Z", "", "exp"},
		{`{"nbf":1700000000}`, exp - 1, false, true, "2023-11-14T22:13:20Z", "", "nbf"},
		{`{"nbf":1700000000.25}`, exp, false, true, "2023-11-14T22:13:20.25Z", "", "nbf"},
		{`{"iat":-1}`, exp, false, false, "1969-12-31T23:59:59Z", "", "iat"},
		{`{"iat":0.001}`, exp, false, false, "1970-01-01T00:00:00.001Z", "", "iat"},
		{`{"exp":"1700000000"}`, exp, false, false, "", "but it is a string", "exp"},
		{`{"exp":{"at":1}}`, exp, false, false, "", "but it is an object", "exp"},
		{`{"exp":1e20}`, exp, false, false, "", "too large", "exp"},
		{`{"exp":300000000000}`, exp, false, false, "", "outside the years", "exp"},
		{`{"exp":1, "exp":1700000000}`, exp - 1, false, false, "2023-11-14T22:13:20Z", "", "exp"}, // the last wins
	} {
		j, err := DecodeJWT(unsignedToken(tc.payload), tc.now)
		if err != nil {
			t.Fatalf("%s: %v", tc.payload, err)
		}
		if len(j.Times) != 1 {
			t.Fatalf("%s: times %+v", tc.payload, j.Times)
		}
		c := j.Times[0]
		if j.Expired != tc.expired || j.NotYetValid != tc.notYet || c.Name != tc.name || c.UTC != tc.utc || !strings.Contains(c.Problem, tc.problem) || (tc.problem == "") != (c.Problem == "") {
			t.Errorf("%s at %d: expired %v, not yet %v, claim %+v", tc.payload, tc.now, j.Expired, j.NotYetValid, c)
		}
	}
	j, _ := DecodeJWT(unsignedToken(`{"nbf":1,"iat":2,"exp":3,"x":{"exp":4}}`), exp)
	if len(j.Times) != 3 || j.Times[0].Name != "exp" || j.Times[0].Unix != 3 || j.Times[1].Name != "iat" || j.Times[2].Name != "nbf" {
		t.Errorf("order or nesting: %+v", j.Times)
	}
}

// TestRFC3339 compares the clock-free date conversion with package time.
func TestRFC3339(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	secs := []int64{0, -1, 951782400, 4107542400, -62167219200, 253402300799, -62167219200 + 86400*60}
	for i := 0; i < 20000; i++ {
		secs = append(secs, r.Int64N(253402300799+62167219200)-62167219200)
	}
	for _, s := range secs {
		got, ok := rfc3339(s, "")
		if want := time.Unix(s, 0).UTC().Format("2006-01-02T15:04:05Z"); !ok || got != want {
			t.Fatalf("rfc3339(%d) = %q, %v; want %q", s, got, ok, want)
		}
	}
	if _, ok := rfc3339(253402300800, ""); ok {
		t.Error("year 10000 accepted")
	}
	if _, ok := rfc3339(-62167219201, ""); ok {
		t.Error("year -1 accepted")
	}
}

func TestDecodeJWTErrors(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	for _, tc := range []struct {
		in   string
		code Code
		col  int
		msg  string
	}{
		{"", CodeInvalidJWT, 1, "empty"},
		{"abc", CodeInvalidJWT, 1, "this has 1"},
		{"a.b", CodeInvalidJWT, 1, "this has 2"},
		{"a.b.c.d.e", CodeInvalidJWT, 1, "encrypted"},
		{".b.c", CodeInvalidJWT, 1, "header part is empty"},
		{enc([]byte("{}")) + ".ab+c.", CodeInvalidBase64, 7, "in the payload, a JWT uses base64url"},
		{"ab$c.e30.", CodeInvalidBase64, 3, "in the header, $ is not"},
		{enc([]byte("{}")) + ".e30.abcde", CodeInvalidBase64, 13, "in the signature"},
	} {
		_, err := DecodeJWT([]byte(tc.in), 0)
		e := checkError(t, []byte(tc.in), err)
		if e.Code != tc.code || e.Pos.Column != tc.col || !strings.Contains(e.Message, tc.msg) {
			t.Errorf("DecodeJWT(%q) = %v (%s), want %s at column %d with %q", tc.in, e, e.Code, tc.code, tc.col, tc.msg)
		}
	}
	// A part that decodes but isn't JSON is reported, not fatal.
	j, err := DecodeJWT([]byte(enc([]byte("not json"))+"."+enc([]byte(`{"a":1,}`))+"."), 0)
	if err != nil || j.HeaderError == nil || j.PayloadError == nil || j.PayloadError.Code != CodeTrailingComma || string(j.Header) != "not json" {
		t.Errorf("got %+v, %v", j, err)
	}
}

func TestEncodeJWT(t *testing.T) {
	// Formatting first: the header and payload may come pretty-printed.
	tok, err := EncodeJWT([]byte("{\n  \"alg\": \"HS256\",\n  \"typ\": \"JWT\"\n}"), []byte(samplePayload), []byte(sampleSecret))
	if err != nil || string(tok) != sampleToken {
		t.Fatalf("got %s, %v\nwant %s", tok, err, sampleToken)
	}
	for alg, h := range map[string]func() hash.Hash{"HS256": sha256.New, "HS384": sha512.New384, "HS512": sha512.New} {
		tok, err := EncodeJWT([]byte(`{"alg":"`+alg+`"}`), []byte(`{"x":"é"}`), []byte("k"))
		if err != nil {
			t.Fatal(err)
		}
		dot := strings.LastIndexByte(string(tok), '.')
		m := hmac.New(h, []byte("k"))
		m.Write(tok[:dot])
		if got := string(tok[dot+1:]); got != base64.RawURLEncoding.EncodeToString(m.Sum(nil)) {
			t.Errorf("%s signature %s differs from crypto/hmac", alg, got)
		}
		j, err := DecodeJWT(tok, 0)
		if err != nil || j.Alg != alg || string(j.Payload) != `{"x":"é"}` || j.SignatureLen != m.Size() {
			t.Errorf("%s round trip: %+v, %v", alg, j, err)
		}
	}
	tok, err = EncodeJWT([]byte(`{"alg":"none"}`), []byte(`{}`), nil)
	if err != nil || string(tok) != "eyJhbGciOiJub25lIn0.e30." {
		t.Errorf("unsigned: %s, %v", tok, err)
	}
	for _, tc := range []struct{ header, payload, secret, msg string }{
		{`{"typ":"JWT"}`, `{}`, "k", `needs an "alg"`},
		{`{"alg":"RS256"}`, `{}`, "k", `"RS256" needs a key`},
		{`{"alg":"HS256"}`, `{}`, "", "needs a secret"},
		{`{"alg":"none"}`, `{}`, "k", "no secret"},
		{`["alg"]`, `{}`, "k", "header must be a JSON object"},
		{`{"alg":"HS256"}`, `"text"`, "k", "payload must be a JSON object"},
		{`{"alg":"HS256"}`, `{"a":1,}`, "k", "in the payload, remove this comma"},
	} {
		_, err := EncodeJWT([]byte(tc.header), []byte(tc.payload), []byte(tc.secret))
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("EncodeJWT(%s, %s, %q) = %v, want %q", tc.header, tc.payload, tc.secret, err, tc.msg)
		}
	}
}
