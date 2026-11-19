package jqo

import (
	"encoding/base64"
	"unicode/utf8"
)

// DefaultMaxLayers is how many string literals DecodeString unwraps at most.
// Real double encoding rarely goes past three layers; the cap only stops
// pathological input early.
const DefaultMaxLayers = 10

// DecodeString returns the contents of a JSON string literal, so that
// "{\"a\":1}" gives {"a":1}. While the result is again a string literal whose
// contents are JSON, it unwraps another layer, up to maxLayers (0 or less
// means DefaultMaxLayers), because JSON encoded twice or three times is a
// common accident in logs; layers counts the literals removed. It stops at a
// string whose contents are not JSON: "\"hello\"" gives "hello", which is
// JSON, rather than hello, which is not. A lone surrogate escape has no UTF-8
// form, so it is an error (CodeLoneSurrogate) rather than a silent U+FFFD.
// Input longer than DefaultMaxSize is refused.
func DecodeString(data []byte, maxLayers int) (out []byte, layers int, err error) {
	defer guard2(&out, &layers, &err)
	return decodeString(data, maxLayers)
}

func decodeString(data []byte, maxLayers int) ([]byte, int, error) {
	if len(data) > DefaultMaxSize {
		return nil, 0, tooLarge("the input", len(data), DefaultMaxSize)
	}
	if maxLayers <= 0 {
		maxLayers = DefaultMaxLayers
	}
	out, e := unquoteAll(data)
	if e != nil {
		return nil, 0, finish(data, e)
	}
	layers := 1
	for layers < maxLayers {
		inner, e := unquoteAll(out)
		if e != nil || !isJSON(inner) {
			break
		}
		out = inner
		layers++
	}
	return out, layers, nil
}

// unquoteAll decodes data that must be one string literal and nothing else
// but whitespace. The error it returns knows only its offset.
func unquoteAll(data []byte) ([]byte, *Error) {
	i := skipSpaces(data, 0)
	if i == len(data) || data[i] != '"' {
		msg := "expected a JSON string literal: text in double quotes"
		if i < len(data) {
			switch data[i] {
			case '\'':
				msg = "a JSON string literal needs double quotes (\"), not single quotes (')"
			case '{', '[':
				msg = "this is already JSON, not a string literal holding JSON; parse or format it directly"
			}
		}
		return nil, errorAt(CodeNotAString, min(i, len(data)), msg)
	}
	s := &scanner{data: data, maxDepth: DefaultMaxDepth, wantSurrogates: true}
	end, _, ok := s.lexString(i)
	if !ok {
		return nil, s.err
	}
	if j := skipSpaces(data, end); j < len(data) {
		return nil, errorAt(CodeExtraData, j, "unexpected text after the closing quote of the string")
	}
	if len(s.surrogates) > 0 {
		at := s.surrogates[0]
		return nil, errorAt(CodeLoneSurrogate, at, string(data[at:at+6])+" is half of a surrogate pair; on its own it has no text form, so the string can't be decoded")
	}
	out, _ := appendUnquoted(nil, data[i+1:end-1])
	return out, nil
}

// isJSON reports whether b is one strictly valid JSON value.
func isJSON(b []byte) bool {
	s := newScanner(b, false, DefaultMaxDepth)
	for {
		switch s.next().kind {
		case tokEOF:
			return true
		case tokError:
			return false
		}
	}
}

// DecodeBase64 decodes standard or URL-safe base64 (RFC 4648), padded or
// not, skipping spaces and line breaks as found in wrapped e-mail or PEM
// text. The alphabet is read from the input; mixing - or _ with + or / is an
// error, since no encoder produces that. Input longer than DefaultMaxSize is
// refused.
func DecodeBase64(data []byte) (out []byte, err error) {
	defer guard(&out, &err)
	if len(data) > DefaultMaxSize {
		return nil, tooLarge("the input", len(data), DefaultMaxSize)
	}
	clean, url, e := cleanBase64(data, false)
	if e != nil {
		return nil, finish(data, e)
	}
	return decodeClean(clean, url)
}

// cleanBase64 checks base64 text and returns it without whitespace and
// padding, and whether it uses the URL-safe alphabet. A JWT part allows
// neither whitespace nor the standard alphabet.
func cleanBase64(data []byte, jwtPart bool) ([]byte, bool, *Error) {
	clean := make([]byte, 0, len(data))
	urlAt, stdAt, padAt, pads, last := -1, -1, -1, 0, 0
	for i, c := range data {
		switch {
		case !jwtPart && (isSpace(c) || c == '\f' || c == '\v'):
			continue
		case c == '=':
			if padAt < 0 {
				padAt = i
			}
			pads++
			last = i
			continue
		case padAt >= 0:
			return nil, false, errorAt(CodeInvalidBase64, i, "= padding can only come at the very end")
		case isLetter(c) || isDigit(c):
		case c == '-' || c == '_':
			if urlAt < 0 {
				urlAt = i
			}
		case (c == '+' || c == '/') && !jwtPart:
			if stdAt < 0 {
				stdAt = i
			}
		default:
			msg := "this is not a base64 character"
			if r, _ := utf8.DecodeRune(data[i:]); r != utf8.RuneError {
				msg = charName(r) + " is not a base64 character"
			}
			if jwtPart && (c == '+' || c == '/') {
				msg = "a JWT uses base64url, where - and _ take the place of + and /"
			}
			return nil, false, errorAt(CodeInvalidBase64, i, msg)
		}
		clean = append(clean, c)
		last = i
	}
	switch {
	case urlAt >= 0 && stdAt >= 0:
		return nil, false, errorAt(CodeInvalidBase64, max(urlAt, stdAt), "this mixes the two base64 alphabets: + and / belong to standard base64, - and _ to base64url")
	case pads > 2 || pads > 0 && (len(clean)+pads)%4 != 0:
		return nil, false, errorAt(CodeInvalidBase64, padAt, "the = padding doesn't fit: padded base64 comes in groups of four characters")
	case len(clean)%4 == 1:
		return nil, false, errorAt(CodeInvalidBase64, last, "the base64 text has one character too many or too few, so it can't be decoded")
	}
	return clean, urlAt >= 0, nil
}

func decodeClean(clean []byte, url bool) ([]byte, error) {
	enc := base64.RawStdEncoding
	if url {
		enc = base64.RawURLEncoding
	}
	out := make([]byte, enc.DecodedLen(len(clean)))
	n, err := enc.Decode(out, clean)
	if err != nil { // cleanBase64 checked characters and length; this is a bug
		return nil, internalError(err)
	}
	return out[:n], nil
}

// DecodePercent undoes URL percent-encoding (RFC 3986), so %7B becomes {. A +
// becomes a space, as in query strings, where JSON in a URL usually sits,
// unless keepPlus is set. Input longer than DefaultMaxSize is refused.
func DecodePercent(data []byte, keepPlus bool) (out []byte, err error) {
	defer guard(&out, &err)
	if len(data) > DefaultMaxSize {
		return nil, tooLarge("the input", len(data), DefaultMaxSize)
	}
	out = make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case c == '%':
			if i+2 >= len(data) || !isHex(data[i+1]) || !isHex(data[i+2]) {
				return nil, finish(data, errorAt(CodeInvalidPercent, i, "% must be followed by two hexadecimal digits; a % sign itself is written %25"))
			}
			out = append(out, unhex(data[i+1])<<4|unhex(data[i+2]))
			i += 2
		case c == '+' && !keepPlus:
			out = append(out, ' ')
		default:
			out = append(out, c)
		}
	}
	return out, nil
}

func unhex(c byte) byte {
	switch {
	case isDigit(c):
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}
