package jqo

import (
	"bytes"
	"encoding/base64"
	"math"
	"strconv"
	"strings"

	"github.com/wisebrian/jqo/internal/sha2"
)

// JWT is a decoded JSON Web Token (RFC 7519). Its signature is never checked:
// anyone can write a token with any payload, so nothing here says the token
// is genuine.
type JWT struct {
	// Header and Payload are the decoded JSON text of the first two parts.
	Header, Payload []byte
	// HeaderError and PayloadError are set when that part is not valid JSON;
	// their positions are within the decoded text.
	HeaderError, PayloadError *Error
	// Alg is the header's "alg", such as "HS256"; empty when it has none.
	Alg string
	// Times holds the exp, iat and nbf claims that are present, in that order.
	Times []TimeClaim
	// Expired reports that exp is present and not after now.
	Expired bool
	// NotYetValid reports that nbf is present and after now.
	NotYetValid bool
	// SignatureLen is the size of the decoded signature; 0 for an unsecured
	// token (alg "none").
	SignatureLen int
	// SignatureChecked is always false. It is there so the UI, and the JSON,
	// say outright that the signature was not checked instead of leaving it
	// to be inferred.
	SignatureChecked bool
}

// TimeClaim is a NumericDate claim made readable.
type TimeClaim struct {
	Name  string // "exp", "iat" or "nbf"
	Value string // the value as written in the payload
	Unix  int64  // whole seconds since 1970-01-01T00:00:00Z
	UTC   string // RFC 3339, such as "2026-11-19T21:30:00Z"
	Past  bool   // before now
	// Problem says why Value isn't a usable date, for instance because it is
	// a string; it is empty when the date is fine.
	Problem string
}

// DecodeJWT splits a compact JWT and decodes its header, payload and time
// claims, judged against now in seconds since the Unix epoch. The caller
// passes now because this package never reads the clock: the same arguments
// always give the same result. A leading "Bearer " is skipped, since tokens
// are often copied from HTTP headers. A part that isn't base64url is an
// error; a header or payload that isn't JSON is reported in HeaderError or
// PayloadError, so the rest can still be shown.
func DecodeJWT(token []byte, now int64) (j *JWT, err error) {
	defer guard(&j, &err)
	return decodeJWT(token, now)
}

// jwtParts finds the dot-separated parts of a token, as offsets into it,
// and how many there are, which names the token even when it can't be read.
func jwtParts(token []byte) ([][2]int, int, *Error) {
	start := skipSpaces(token, 0)
	end := len(token)
	for end > start && isSpace(token[end-1]) {
		end--
	}
	if end-start > 7 && lowerASCII(string(token[start:start+6])) == "bearer" && isSpace(token[start+6]) {
		start = skipSpaces(token, start+6)
	}
	var parts [][2]int
	from := start
	for i := start; i <= end; i++ {
		if i == end || token[i] == '.' {
			parts = append(parts, [2]int{from, i})
			from = i + 1
		}
	}
	switch {
	case start == end:
		return nil, len(parts), errorAt(CodeInvalidJWT, start, "the token is empty")
	case len(parts) == 5:
		return nil, len(parts), errorAt(CodeInvalidJWT, start, "this is an encrypted token (JWE, five parts); its contents can't be read without the key")
	case len(parts) != 3:
		return nil, len(parts), errorAt(CodeInvalidJWT, start, "a JWT has three parts separated by dots (header.payload.signature); this has "+strconv.Itoa(len(parts)))
	case parts[0][0] == parts[0][1]:
		return nil, len(parts), errorAt(CodeInvalidJWT, start, "the header part is empty")
	}
	return parts, len(parts), nil
}

func decodeJWT(token []byte, now int64) (*JWT, error) {
	if len(token) > DefaultMaxSize {
		return nil, tooLarge("the token", len(token), DefaultMaxSize)
	}
	parts, _, e := jwtParts(token)
	if e != nil {
		return nil, finish(token, e)
	}
	var dec [3][]byte
	for i, p := range parts {
		clean, _, e := cleanBase64(token[p[0]:p[1]], true)
		if e != nil {
			e.Pos.Offset += p[0]
			e.Message = [...]string{"in the header, ", "in the payload, ", "in the signature, "}[i] + e.Message
			return nil, finish(token, e)
		}
		if dec[i], e = decodeRawURL(clean); e != nil {
			return nil, e
		}
	}
	j := &JWT{Header: dec[0], Payload: dec[1], SignatureLen: len(dec[2])}
	if _, err := parse(j.Header, Options{}); err != nil {
		j.HeaderError = err.(*Error)
	} else if v, ok := lastMember(j.Header, "alg"); ok && v.kind == tokString {
		j.Alg = string(keyText(j.Header, v))
	}
	if _, err := parse(j.Payload, Options{}); err != nil {
		j.PayloadError = err.(*Error)
		return j, nil
	}
	for _, name := range [...]string{"exp", "iat", "nbf"} {
		v, ok := lastMember(j.Payload, name)
		if !ok {
			continue
		}
		c, sec, frac := timeClaim(name, j.Payload, v, now)
		j.Times = append(j.Times, c)
		if c.Problem != "" {
			continue
		}
		switch name {
		case "exp":
			j.Expired = sec < now || sec == now && !frac
		case "nbf":
			j.NotYetValid = sec > now || sec == now && frac
		}
	}
	return j, nil
}

func decodeRawURL(clean []byte) ([]byte, *Error) {
	out := make([]byte, base64.RawURLEncoding.DecodedLen(len(clean)))
	n, err := base64.RawURLEncoding.Decode(out, clean)
	if err != nil { // cleanBase64 checked the characters and the length
		return nil, internalError(err)
	}
	return out[:n], nil
}

// lastMember finds a member of the top-level object in valid JSON. A
// duplicate name gives the last one, as RFC 7519 asks of parsers that don't
// reject duplicates. v spans the whole value, nested or not.
func lastMember(data []byte, name string) (v token, ok bool) {
	s := newScanner(data, false, DefaultMaxDepth)
	if s.next().kind != tokBeginObject {
		return token{}, false
	}
	for {
		k := s.next()
		if k.kind != tokKey {
			return v, ok
		}
		match := string(keyText(data, k)) == name
		t := s.next()
		if t.kind == tokBeginObject || t.kind == tokBeginArray {
			for depth := 1; depth > 0; {
				switch e := s.next(); e.kind {
				case tokBeginObject, tokBeginArray:
					depth++
				case tokEndObject, tokEndArray:
					depth--
					t.end = e.end
				case tokError, tokEOF:
					return v, ok
				}
			}
		}
		if match {
			v, ok = t, true
		}
	}
}

// timeClaim reads a NumericDate: seconds since the epoch, possibly with a
// fraction. sec is the whole seconds and frac whether there is a fraction,
// for comparing with now.
func timeClaim(name string, data []byte, v token, now int64) (c TimeClaim, sec int64, frac bool) {
	raw := string(data[v.start:v.end])
	c = TimeClaim{Name: name, Value: clip(raw, 60)}
	if v.kind != tokNumber {
		what := map[tokKind]string{tokString: "a string", tokTrue: "true", tokFalse: "false", tokNull: "null",
			tokBeginObject: "an object", tokBeginArray: "an array"}[v.kind]
		c.Problem = name + " should be a number of seconds since 1970, but it is " + what
		return c, 0, false
	}
	sec, digits, ok := numericDate(raw)
	if !ok {
		c.Problem = "the number is too large to be a date"
		return c, 0, false
	}
	c.Unix = sec
	c.Past = sec < now
	if c.UTC, ok = rfc3339(sec, digits); !ok {
		c.Problem = "the date is outside the years 0000 to 9999"
	}
	return c, sec, digits != ""
}

// numericDate splits a number literal into whole seconds and the digits of
// its fraction. Plain literals are read exactly, as text; others go through
// float64 and keep milliseconds.
func numericDate(lit string) (sec int64, frac string, ok bool) {
	if !strings.ContainsAny(lit, "eE-") {
		whole, f, _ := strings.Cut(lit, ".")
		n, err := strconv.ParseInt(whole, 10, 64)
		if err != nil {
			return 0, "", false
		}
		f = strings.TrimRight(f, "0")
		return n, f[:min(len(f), 9)], true
	}
	v, err := strconv.ParseFloat(lit, 64)
	if err != nil || math.Abs(v) > 1e15 {
		return 0, "", false
	}
	whole := math.Floor(v)
	ms := int64(math.Round((v - whole) * 1000))
	if ms == 1000 {
		whole, ms = whole+1, 0
	}
	if ms > 0 {
		frac = strings.TrimRight(strconv.FormatInt(1000+ms, 10)[1:], "0")
	}
	return int64(whole), frac, true
}

// rfc3339 writes seconds since the epoch as an RFC 3339 date in UTC, without
// package time, which the core doesn't import because it can read the clock.
func rfc3339(sec int64, frac string) (string, bool) {
	const first, last = -62167219200, 253402300799 // 0000-01-01T00:00:00Z, 9999-12-31T23:59:59Z
	if sec < first || sec > last {
		return "", false
	}
	days := sec / 86400
	rem := sec % 86400
	if rem < 0 {
		days--
		rem += 86400
	}
	y, m, d := civil(days)
	b := make([]byte, 0, 32)
	b = appendDigits(b, y, 4)
	b = append(b, '-')
	b = appendDigits(b, int64(m), 2)
	b = append(b, '-')
	b = appendDigits(b, int64(d), 2)
	b = append(b, 'T')
	b = appendDigits(b, rem/3600, 2)
	b = append(b, ':')
	b = appendDigits(b, rem%3600/60, 2)
	b = append(b, ':')
	b = appendDigits(b, rem%60, 2)
	if frac != "" {
		b = append(append(b, '.'), frac...)
	}
	return string(append(b, 'Z')), true
}

// civil converts days since 1970-01-01 to a proleptic Gregorian date, after
// Howard Hinnant's days_from_civil inverse.
func civil(days int64) (y int64, m, d int) {
	z := days + 719468
	era := z / 146097
	if z < 0 && z%146097 != 0 {
		era--
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = int(doy - (153*mp+2)/5 + 1)
	m = int(mp + 3)
	if m > 12 {
		m -= 12
	}
	y = yoe + era*400
	if m <= 2 {
		y++
	}
	return y, m, d
}

func appendDigits(b []byte, n int64, width int) []byte {
	s := strconv.FormatInt(n, 10)
	for i := len(s); i < width; i++ {
		b = append(b, '0')
	}
	return append(b, s...)
}

// EncodeJWT builds a compact JWT from two JSON objects, minified first
// because tokens travel in headers and URLs. The header's "alg" picks the
// signature: "none" leaves it empty, and HS256, HS384 and HS512 sign with
// secret. Any other algorithm is refused rather than producing a token that
// looks signed but isn't. Secrets shorter than the hash (32 bytes for HS256)
// are accepted, as most libraries accept them, although RFC 7518 asks for at
// least that length.
func EncodeJWT(header, payload, secret []byte) (token []byte, err error) {
	defer guard(&token, &err)
	return encodeJWT(header, payload, secret)
}

func encodeJWT(header, payload, secret []byte) ([]byte, error) {
	h, err := jwtObject(header, "header")
	if err != nil {
		return nil, err
	}
	p, err := jwtObject(payload, "payload")
	if err != nil {
		return nil, err
	}
	if len(secret) > DefaultMaxSize {
		return nil, tooLarge("the secret", len(secret), DefaultMaxSize)
	}
	alg := ""
	if v, ok := lastMember(h, "alg"); ok && v.kind == tokString {
		alg = string(keyText(h, v))
	}
	var mac func(key, msg []byte) []byte
	switch alg {
	case "none":
		if len(secret) > 0 {
			return nil, &Error{Code: CodeInvalidJWT, Message: "alg \"none\" means no signature, so there should be no secret"}
		}
	case "HS256":
		mac = sha2.HMAC256
	case "HS384":
		mac = sha2.HMAC384
	case "HS512":
		mac = sha2.HMAC512
	case "":
		return nil, &Error{Code: CodeInvalidJWT, Message: "the header needs an \"alg\" member: \"HS256\", \"HS384\", \"HS512\" or \"none\""}
	default:
		return nil, &Error{Code: CodeInvalidJWT, Message: "this tool signs only with HS256, HS384 and HS512, or not at all with \"none\"; \"" + clip(alg, 20) + "\" needs a key it doesn't handle"}
	}
	if mac != nil && len(secret) == 0 {
		return nil, &Error{Code: CodeInvalidJWT, Message: alg + " needs a secret to sign with"}
	}
	enc := base64.RawURLEncoding
	out := enc.AppendEncode(nil, h)
	out = append(out, '.')
	out = enc.AppendEncode(out, p)
	signed := len(out)
	out = append(out, '.')
	if mac != nil {
		out = enc.AppendEncode(out, mac(secret, out[:signed]))
	}
	return out, nil
}

// jwtObject checks that a header or payload is a JSON object and minifies it.
func jwtObject(data []byte, part string) ([]byte, error) {
	res, err := format(data, FormatOptions{Style: Minify})
	if err != nil {
		e := err.(*Error)
		e.Message = "in the " + part + ", " + e.Message
		return nil, e
	}
	if !bytes.HasPrefix(res.Output, []byte("{")) {
		return nil, &Error{Code: CodeInvalidJWT, Message: "the " + part + " must be a JSON object, in { and }"}
	}
	return res.Output, nil
}
