package jqo

import (
	"encoding/base64"
	"unicode/utf8"
)

// EncodeString writes text as a JSON string literal, to paste into source
// code or into another JSON document. U+2028 and U+2029 are always escaped:
// they are valid in JSON but end a line in JavaScript source before ES2019.
// Text that isn't valid UTF-8 is an error rather than patched with U+FFFD,
// so that DecodeString always gives back exactly the input. Input longer
// than DefaultMaxSize is refused.
func EncodeString(text []byte, esc Escape) (out []byte, err error) {
	defer guard(&out, &err)
	if len(text) > DefaultMaxSize {
		return nil, tooLarge("the input", len(text), DefaultMaxSize)
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, finish(text, errorAt(CodeInvalidUTF8, i, "this byte is not valid UTF-8 text, so it can't go into a JSON string"))
		}
		i += size
	}
	return appendQuoted(make([]byte, 0, len(text)+2), text, esc), nil
}

// appendQuoted writes valid UTF-8 text as a JSON string literal.
func appendQuoted(dst, text []byte, esc Escape) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(text); {
		c := text[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && !(esc.HTML && (c == '<' || c == '>' || c == '&')) {
				i++
				continue
			}
			dst = append(dst, text[start:i]...)
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			default:
				dst = appendEscape(dst, rune(c))
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRune(text[i:])
		if esc.NonASCII || r == 0x2028 || r == 0x2029 {
			dst = append(dst, text[start:i]...)
			dst = appendEscape(dst, r)
			start = i + size
		}
		i += size
	}
	dst = append(dst, text[start:]...)
	return append(dst, '"')
}

// Base64Options choose the base64 variant EncodeBase64 writes.
type Base64Options struct {
	URL       bool // - and _ instead of + and /, as in JWTs and URLs
	NoPadding bool // leave out the trailing =, as JWTs do
}

// EncodeBase64 encodes data as base64 (RFC 4648). Input longer than
// DefaultMaxSize is refused.
func EncodeBase64(data []byte, opts Base64Options) (out []byte, err error) {
	defer guard(&out, &err)
	if len(data) > DefaultMaxSize {
		return nil, tooLarge("the input", len(data), DefaultMaxSize)
	}
	enc := base64.StdEncoding
	switch {
	case opts.URL && opts.NoPadding:
		enc = base64.RawURLEncoding
	case opts.URL:
		enc = base64.URLEncoding
	case opts.NoPadding:
		enc = base64.RawStdEncoding
	}
	return enc.AppendEncode(make([]byte, 0, enc.EncodedLen(len(data))), data), nil
}

// EncodePercent percent-encodes every byte except the RFC 3986 unreserved
// characters (A-Z a-z 0-9 - . _ ~), so the result is safe anywhere in a URL.
// A space becomes %20, not +, because %20 means a space everywhere and +
// only in query strings. Input longer than DefaultMaxSize is refused.
func EncodePercent(data []byte) (out []byte, err error) {
	defer guard(&out, &err)
	if len(data) > DefaultMaxSize {
		return nil, tooLarge("the input", len(data), DefaultMaxSize)
	}
	const upper = "0123456789ABCDEF"
	out = make([]byte, 0, len(data)*3/2)
	for _, c := range data {
		if isLetter(c) || isDigit(c) || c == '-' || c == '.' || c == '_' || c == '~' {
			out = append(out, c)
		} else {
			out = append(out, '%', upper[c>>4], upper[c&0xF])
		}
	}
	return out, nil
}
