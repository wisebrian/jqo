package jqo

import (
	"bytes"
	"slices"
	"strconv"
	"unicode/utf8"
)

// Encoding names what Detect recognized.
type Encoding string

// The encodings Detect can name, each with the function that undoes it.
const (
	EncJSON    Encoding = "json"        // already JSON: Parse or Format it
	EncString  Encoding = "json-string" // DecodeString
	EncBase64  Encoding = "base64"      // DecodeBase64, standard or URL-safe
	EncPercent Encoding = "percent"     // DecodePercent
	EncJWT     Encoding = "jwt"         // DecodeJWT
)

// Guess is one candidate from Detect.
type Guess struct {
	Encoding Encoding // what to decode the input with
	// Confidence is "high", "medium" or "low". It is a word rather than a
	// number because it is meant for choosing what to suggest, not for
	// arithmetic.
	Confidence string
	// Reason explains the guess in a sentence for the UI.
	Reason string
}

// MarshalJSON writes {"encoding","confidence","reason"}.
func (g Guess) MarshalJSON() ([]byte, error) { return g.appendJSON(nil), nil }

func (g Guess) appendJSON(b []byte) []byte {
	o := newObject(b)
	o.str("encoding", string(g.Encoding))
	o.str("confidence", g.Confidence)
	o.str("reason", g.Reason)
	return o.end()
}

// MarshalGuesses writes guesses as a JSON array, for the WebAssembly bridge.
func MarshalGuesses(guesses []Guess) []byte {
	return appendList(nil, len(guesses), func(b []byte, i int) []byte { return guesses[i].appendJSON(b) })
}

// detectPrefix is how much of the input Detect trial-decodes. A prefix is
// enough to tell JSON from binary data, and it keeps Detect instant on large
// pastes.
const detectPrefix = 64 << 10

// Detect lists the decoders that could apply to data, the most likely
// first, so that the UI can suggest one; an empty list means none fits. It
// returns no decoded data, and the caller always chooses: a guess is never
// acted on.
func Detect(data []byte) (guesses []Guess) {
	defer func() {
		if !rethrow {
			if r := recover(); r != nil {
				guesses = nil
			}
		}
	}()
	return detect(data)
}

func detect(data []byte) []Guess {
	t := bytes.TrimSpace(data)
	if len(t) == 0 || len(data) > DefaultMaxSize {
		return nil
	}
	var gs []Guess
	add := func(e Encoding, confidence, reason string) {
		gs = append(gs, Guess{e, confidence, reason})
	}
	switch {
	case isJSON(t) && t[0] == '"':
		if out, layers, err := decodeString(t, 0); err == nil && isJSON(out) {
			reason := "a JSON string whose text is itself JSON; decoding it gives the JSON inside"
			if layers > 1 {
				reason = "JSON encoded as a string " + strconv.Itoa(layers) + " times over; decoding unwraps every layer"
			}
			add(EncString, "high", reason)
			add(EncJSON, "medium", "valid JSON, but only a single string")
		} else {
			add(EncJSON, "high", "already valid JSON: a single string")
			add(EncString, "low", "a JSON string literal; decoding it gives its text")
		}
		return gs
	case isJSON(t):
		add(EncJSON, "high", "already valid JSON; there is nothing to decode")
		return gs
	case isLenientJSON(t):
		add(EncJSON, "medium", "JSON with comments, trailing commas or a byte order mark, which lenient mode accepts")
		return gs
	}
	if g, ok := guessJWT(t); ok {
		gs = append(gs, g)
	}
	if g, ok := guessBase64(t); ok {
		gs = append(gs, g)
	}
	if g, ok := guessPercent(t); ok {
		gs = append(gs, g)
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	slices.SortStableFunc(gs, func(a, b Guess) int { return rank[a.Confidence] - rank[b.Confidence] })
	return gs
}

func isLenientJSON(b []byte) bool {
	s := newScanner(b, true, DefaultMaxDepth)
	for {
		switch s.next().kind {
		case tokEOF:
			return true
		case tokError:
			return false
		}
	}
}

func guessJWT(t []byte) (Guess, bool) {
	parts, n, e := jwtParts(t)
	if e != nil {
		if n == 5 {
			return Guess{EncJWT, "medium", "an encrypted JWT (JWE): five base64url parts, of which only the header can be read"}, true
		}
		return Guess{}, false
	}
	for _, p := range parts {
		if _, _, e := cleanBase64(t[p[0]:p[1]], true); e != nil {
			return Guess{}, false
		}
	}
	clean, _, _ := cleanBase64(t[parts[0][0]:parts[0][1]], true)
	if header, e := decodeRawURL(clean); e == nil && bytes.HasPrefix(header, []byte("{")) && isJSON(header) {
		return Guess{EncJWT, "high", "a JSON Web Token: three base64url parts separated by dots, and the first decodes to a JSON header"}, true
	}
	return Guess{EncJWT, "low", "three base64url parts separated by dots, like a JWT, but the first doesn't decode to JSON"}, true
}

func guessBase64(t []byte) (Guess, bool) {
	clean, url, e := cleanBase64(t, false)
	if e != nil || len(clean) < 4 {
		return Guess{}, false
	}
	name := "base64"
	if url {
		name = "base64url"
	}
	n := min(len(clean), detectPrefix) &^ 3
	prefix, err := decodeClean(clean[:n], url)
	if err != nil {
		return Guess{}, false
	}
	switch {
	case looksLikeJSON(prefix):
		return Guess{EncBase64, "high", name + " that decodes to JSON"}, true
	case bytes.HasPrefix(prefix, []byte{0x1f, 0x8b}):
		return Guess{EncBase64, "low", name + " that decodes to gzip-compressed data, which this tool doesn't unpack"}, true
	case validPrefix(prefix):
		return Guess{EncBase64, "medium", name + " that decodes to text, but not to JSON"}, true
	case bytes.IndexByte(t, ' ') >= 0:
		// Words separated by spaces happen to use only base64 letters;
		// wrapped base64 breaks lines instead.
		return Guess{}, false
	}
	return Guess{EncBase64, "low", "only base64 characters, but they decode to binary data rather than text"}, true
}

func guessPercent(t []byte) (Guess, bool) {
	escapes := 0
	for i := 0; i+2 < len(t); i++ {
		if t[i] == '%' && isHex(t[i+1]) && isHex(t[i+2]) {
			escapes++
		}
	}
	if escapes == 0 {
		return Guess{}, false
	}
	out, err := DecodePercent(t[:min(len(t), detectPrefix)], false)
	switch {
	case err != nil:
		return Guess{EncPercent, "low", "it has percent escapes such as %7B, but also a % that isn't one"}, true
	case looksLikeJSON(out):
		return Guess{EncPercent, "high", "URL percent-encoding (such as %7B and %22) that decodes to JSON"}, true
	case bytes.ContainsAny(out, "{["):
		return Guess{EncPercent, "medium", "URL percent-encoding with JSON somewhere inside; if this is a whole URL, decode just the value after the ="}, true
	}
	return Guess{EncPercent, "medium", "URL percent-encoding, but the result doesn't look like JSON"}, true
}

// looksLikeJSON reports whether decoded bytes start like a JSON object,
// array or string and are text.
func looksLikeJSON(b []byte) bool {
	b = bytes.TrimLeft(bytes.TrimPrefix(b, bom), " \t\r\n")
	return len(b) > 0 && (b[0] == '{' || b[0] == '[' || b[0] == '"') && validPrefix(b)
}

// validPrefix is utf8.Valid for a prefix that may end inside a character.
func validPrefix(b []byte) bool {
	for i := max(0, len(b)-3); i < len(b); i++ {
		if utf8.RuneStart(b[i]) && !utf8.FullRune(b[i:]) {
			b = b[:i]
			break
		}
	}
	return utf8.Valid(b)
}
