package jqo

import (
	"strconv"
	"unicode/utf8"
)

// Small helpers for building messages. They avoid package fmt, which would
// add about 130 KB to the WebAssembly binary and pull in package os.

const hexDigits = "0123456789abcdef"

// uPlus writes r as U+XXXX, the way Unicode charts name characters.
func uPlus(r rune) string {
	s := strconv.FormatInt(int64(r), 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return "U+" + upperHex(s)
}

func upperHex(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'f' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// charName describes a character for a message, spelling out the ones that
// would otherwise be invisible.
func charName(r rune) string {
	switch {
	case r == ' ':
		return "a space"
	case r == utf8.RuneError:
		return "an invalid byte"
	case r < 0x20 || r == 0x7F || r >= 0x80 && r < 0xA0:
		return "the control character " + uPlus(r)
	case spaceName(r) != "":
		return spaceName(r)
	}
	return string(r)
}

// clip shortens s to at most n characters, marking the cut, so a message
// quoting the input stays one line.
func clip(s string, n int) string {
	i := 0
	for k := 0; k < n; k++ {
		if i >= len(s) {
			return s
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	if i >= len(s) {
		return s
	}
	return s[:i] + "…"
}

// spaceName names the characters that look like a space, or like nothing,
// but are not JSON whitespace. Text copied from web pages, word processors
// and chat apps is full of them, and they make otherwise valid JSON fail in a
// way nobody can see.
func spaceName(r rune) string {
	switch r {
	case 0x00A0:
		return "a no-break space (U+00A0)"
	case 0x00AD:
		return "an invisible soft hyphen (U+00AD)"
	case 0x1680:
		return "an Ogham space mark (U+1680)"
	case 0x200B:
		return "an invisible zero-width space (U+200B)"
	case 0x200C:
		return "an invisible zero-width non-joiner (U+200C)"
	case 0x200D:
		return "an invisible zero-width joiner (U+200D)"
	case 0x2028:
		return "a line separator (U+2028)"
	case 0x2029:
		return "a paragraph separator (U+2029)"
	case 0x202F:
		return "a narrow no-break space (U+202F)"
	case 0x205F:
		return "a mathematical space (U+205F)"
	case 0x2060:
		return "an invisible word joiner (U+2060)"
	case 0x3000:
		return "an ideographic space (U+3000)"
	case 0xFEFF:
		return "an invisible byte order mark (U+FEFF)"
	}
	if 0x2000 <= r && r <= 0x200A {
		return "a typographic space (" + uPlus(r) + ")"
	}
	return ""
}

// isSmartQuote reports the curly and full-width quotes that autocorrect
// substitutes for " and '.
func isSmartQuote(r rune) bool {
	return 0x2018 <= r && r <= 0x201F || r == 0x2032 || r == 0x2033 || r == 0xFF02 || r == 0xFF07
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// isWordStart and isWordPart describe identifiers such as true, NaN or an
// unquoted key, which the scanner reads whole to name the mistake.
func isWordStart(c byte) bool { return isLetter(c) || c == '_' || c == '$' }

func isWordPart(c byte) bool { return isWordStart(c) || isDigit(c) }

// startsValue reports whether c could begin a value, which turns "expected a
// comma" into the likelier "is a comma missing?".
func startsValue(c byte) bool {
	return c == '"' || c == '{' || c == '[' || c == '-' || c == '\'' || isDigit(c) || isWordStart(c)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c - 'A' + 'a'
		}
	}
	return string(b)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func skipSpaces(d []byte, i int) int {
	for i < len(d) && isSpace(d[i]) {
		i++
	}
	return i
}
