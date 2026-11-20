package jqo

import (
	"strconv"
	"unicode/utf8"
)

// The result types marshal themselves by hand. That is the shape the
// WebAssembly bridge hands to JavaScript, and writing it directly keeps
// encoding/json, and with it reflect, fmt and os, out of the binary.

// jsonObject writes the members of one JSON object.
type jsonObject struct {
	b []byte
	n int
}

func newObject(b []byte) *jsonObject { return &jsonObject{b: append(b, '{')} }

func (o *jsonObject) key(k string) {
	if o.n > 0 {
		o.b = append(o.b, ',')
	}
	o.n++
	o.b = appendJSONString(o.b, k)
	o.b = append(o.b, ':')
}

func (o *jsonObject) str(k, v string) {
	o.key(k)
	o.b = appendJSONString(o.b, v)
}

func (o *jsonObject) int(k string, v int) {
	o.key(k)
	o.b = strconv.AppendInt(o.b, int64(v), 10)
}

func (o *jsonObject) bool(k string, v bool) {
	o.key(k)
	o.b = strconv.AppendBool(o.b, v)
}

func (o *jsonObject) raw(k string, v []byte) {
	o.key(k)
	o.b = append(o.b, v...)
}

func (o *jsonObject) end() []byte { return append(o.b, '}') }

// appendList writes a JSON array of n elements, each appended by elem.
func appendList(b []byte, n int, elem func(b []byte, i int) []byte) []byte {
	b = append(b, '[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = elem(b, i)
	}
	return append(b, ']')
}

// appendJSONString writes s as a JSON string that JSON.parse accepts and
// that gives back the same JavaScript string: lone surrogates kept as WTF-8
// become escapes again, and any other invalid byte becomes U+FFFD.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = appendEscape(dst, rune(c))
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			if sur, ok := wtf8Surrogate(s, i); ok {
				dst = appendEscape(dst, sur)
				i += 3
			} else {
				dst = appendEscape(dst, utf8.RuneError)
				i++
			}
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

func appendPosition(b []byte, p Position) []byte {
	o := newObject(b)
	o.int("offset", p.Offset)
	o.int("line", p.Line)
	o.int("column", p.Column)
	o.int("utf16", p.UTF16)
	return o.end()
}

// MarshalJSON writes {"offset","line","column","utf16"}.
func (p Position) MarshalJSON() ([]byte, error) { return appendPosition(nil, p), nil }

func (e *Error) appendJSON(b []byte) []byte {
	o := newObject(b)
	o.str("code", string(e.Code))
	o.str("message", e.Message)
	o.str("text", e.Error())
	o.raw("pos", appendPosition(nil, e.Pos))
	o.str("snippet", e.Snippet)
	o.str("caret", e.Caret)
	return o.end()
}

// MarshalJSON writes {"code","message","text","pos","snippet","caret"}, where
// text is Error(), the message with its position, ready to display.
func (e *Error) MarshalJSON() ([]byte, error) { return e.appendJSON(nil), nil }

func (w Warning) appendJSON(b []byte) []byte {
	o := newObject(b)
	o.str("code", string(w.Code))
	o.str("message", w.Message)
	o.raw("pos", appendPosition(nil, w.Pos))
	if w.First.Line > 0 {
		o.raw("first", appendPosition(nil, w.First))
	}
	return o.end()
}

// MarshalJSON writes {"code","message","pos"}, plus "first" for a duplicate key.
func (w Warning) MarshalJSON() ([]byte, error) { return w.appendJSON(nil), nil }

func (f Fix) appendJSON(b []byte) []byte {
	o := newObject(b)
	o.str("code", string(f.Code))
	o.str("message", f.Message)
	o.raw("pos", appendPosition(nil, f.Pos))
	o.raw("end", appendPosition(nil, f.End))
	return o.end()
}

// MarshalJSON writes {"code","message","pos","end"}.
func (f Fix) MarshalJSON() ([]byte, error) { return f.appendJSON(nil), nil }

func appendFixes(b []byte, fixes []Fix) []byte {
	return appendList(b, len(fixes), func(b []byte, i int) []byte { return fixes[i].appendJSON(b) })
}

func (s Summary) appendJSON(b []byte) []byte {
	o := newObject(b)
	o.int("size", s.Size)
	o.int("maxDepth", s.MaxDepth)
	o.int("objects", s.Objects)
	o.int("arrays", s.Arrays)
	o.int("strings", s.Strings)
	o.int("numbers", s.Numbers)
	o.int("booleans", s.Booleans)
	o.int("nulls", s.Nulls)
	o.int("keys", s.Keys)
	return o.end()
}

// MarshalJSON writes the counts under camelCase names, such as "maxDepth".
func (s Summary) MarshalJSON() ([]byte, error) { return s.appendJSON(nil), nil }

// MarshalJSON writes {"summary","warnings","warningCount","fixes","fixCount"}.
func (r *Report) MarshalJSON() ([]byte, error) {
	o := newObject(nil)
	o.raw("summary", r.Summary.appendJSON(nil))
	o.raw("warnings", appendList(nil, len(r.Warnings), func(b []byte, i int) []byte { return r.Warnings[i].appendJSON(b) }))
	o.int("warningCount", r.WarningCount)
	o.raw("fixes", appendFixes(nil, r.Fixes))
	o.int("fixCount", r.FixCount)
	return o.end(), nil
}

func errorJSON(e *Error) []byte {
	if e == nil {
		return []byte("null")
	}
	return e.appendJSON(nil)
}

func (c TimeClaim) appendJSON(b []byte) []byte {
	o := newObject(b)
	o.str("name", c.Name)
	o.str("value", c.Value)
	o.raw("unix", strconv.AppendInt(nil, c.Unix, 10))
	o.str("utc", c.UTC)
	o.bool("past", c.Past)
	o.str("problem", c.Problem)
	return o.end()
}

// MarshalJSON writes {"name","value","unix","utc","past","problem"}.
func (c TimeClaim) MarshalJSON() ([]byte, error) { return c.appendJSON(nil), nil }

// MarshalJSON writes {"header","payload","headerError","payloadError","alg",
// "times","expired","notYetValid","signatureLength","signatureChecked"}, with
// the header and payload as JSON text in strings and missing errors as null.
func (j *JWT) MarshalJSON() ([]byte, error) {
	o := newObject(nil)
	o.str("header", string(j.Header))
	o.str("payload", string(j.Payload))
	o.raw("headerError", errorJSON(j.HeaderError))
	o.raw("payloadError", errorJSON(j.PayloadError))
	o.str("alg", j.Alg)
	o.raw("times", appendList(nil, len(j.Times), func(b []byte, i int) []byte { return j.Times[i].appendJSON(b) }))
	o.bool("expired", j.Expired)
	o.bool("notYetValid", j.NotYetValid)
	o.int("signatureLength", j.SignatureLen)
	o.bool("signatureChecked", j.SignatureChecked)
	return o.end(), nil
}
