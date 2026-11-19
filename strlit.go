package jqo

import "unicode/utf8"

// hex4 reads the four hex digits of a \u escape at d[i:].
func hex4(d []byte, i int) (rune, bool) {
	if i+4 > len(d) {
		return 0, false
	}
	var r rune
	for _, c := range d[i : i+4] {
		var v byte
		switch {
		case '0' <= c && c <= '9':
			v = c - '0'
		case 'a' <= c && c <= 'f':
			v = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | rune(v)
	}
	return r, true
}

// lowSurrogateAt reports whether d[i:] starts with an escaped low surrogate,
// the second half of a pair such as 😀.
func lowSurrogateAt(d []byte, i int) (rune, bool) {
	if i+6 > len(d) || d[i] != '\\' || d[i+1] != 'u' {
		return 0, false
	}
	r, ok := hex4(d, i+2)
	if !ok || r < 0xDC00 || r > 0xDFFF {
		return 0, false
	}
	return r, true
}

// appendUnquoted appends the text that the body of a valid string literal
// (the part between the quotes) stands for. A lone surrogate escape has no
// UTF-8 form; it is written as the three bytes UTF-8 would use if it allowed
// surrogates (WTF-8), so that sorting and duplicate detection still tell
// different ones apart and the JSON output can restore the escape. lone is
// the offset in body of the first such escape, or -1.
func appendUnquoted(dst, body []byte) (out []byte, lone int) {
	lone = -1
	for i := 0; i < len(body); {
		if body[i] != '\\' {
			j := i + 1
			for j < len(body) && body[j] != '\\' {
				j++
			}
			dst = append(dst, body[i:j]...)
			i = j
			continue
		}
		switch e := body[i+1]; e {
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'u':
			r, _ := hex4(body, i+2)
			if 0xD800 <= r && r < 0xDC00 {
				if lo, ok := lowSurrogateAt(body, i+6); ok {
					dst = utf8.AppendRune(dst, 0x10000+(r-0xD800)<<10|(lo-0xDC00))
					i += 12
					continue
				}
			}
			if 0xD800 <= r && r <= 0xDFFF {
				if lone < 0 {
					lone = i
				}
				dst = append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
			} else {
				dst = utf8.AppendRune(dst, r)
			}
			i += 6
			continue
		default: // " \ /
			dst = append(dst, e)
		}
		i += 2
	}
	return dst, lone
}

// keyText returns what a key or string token stands for, without allocating
// a decoding buffer for the common literal that has no escapes.
func keyText(data []byte, t token) []byte {
	body := data[t.start+1 : t.end-1]
	if !t.escaped {
		return body
	}
	b, _ := appendUnquoted(nil, body)
	return b
}

// wtf8Surrogate decodes the WTF-8 bytes appendUnquoted uses for a lone
// surrogate, so JSON output can write it back as an escape.
func wtf8Surrogate(s string, i int) (rune, bool) {
	if i+2 < len(s) && s[i] == 0xED && 0xA0 <= s[i+1] && s[i+1] <= 0xBF && 0x80 <= s[i+2] && s[i+2] <= 0xBF {
		return 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), true
	}
	return 0, false
}

// appendEscape writes r as \uXXXX, or as a surrogate pair above U+FFFF.
func appendEscape(dst []byte, r rune) []byte {
	if r >= 0x10000 {
		r -= 0x10000
		dst = appendEscape(dst, 0xD800+r>>10)
		return appendEscape(dst, 0xDC00+r&0x3FF)
	}
	return append(dst, '\\', 'u', hexDigits[r>>12&0xF], hexDigits[r>>8&0xF], hexDigits[r>>4&0xF], hexDigits[r&0xF])
}
