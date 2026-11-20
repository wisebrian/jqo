package jqo

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

// tokKind is what the scanner found.
type tokKind uint8

const (
	tokEOF tokKind = iota // the input ended after a complete value
	tokError
	tokBeginObject
	tokEndObject
	tokBeginArray
	tokEndArray
	tokKey // a member name; its colon is checked on the next call
	tokString
	tokNumber
	tokTrue
	tokFalse
	tokNull
)

// token is one step of a scan: a bracket or a literal, with its byte span.
type token struct {
	kind       tokKind
	start, end int
	escaped    bool // a string or key containing a backslash
	integer    bool // a number without fraction or exponent
}

// scanState is what the grammar allows next.
type scanState uint8

const (
	stTop       scanState = iota // the one top-level value
	stValue                      // a member value, after its colon
	stElemFirst                  // the first element, or ] for an empty array
	stElemNext                   // an element after a comma
	stKeyFirst                   // the first key, or } for an empty object
	stKeyNext                    // a key after a comma
	stColon                      // the colon after a key
	stAfter                      // a comma or a closing bracket, after a value in a container
	stDone                       // only whitespace, after the top-level value
)

// frame is an open object or array.
type frame struct {
	open int // offset of its bracket, for messages about where it began
	obj  bool
}

// fixSpan is input that lenient mode accepted and Repair removes.
type fixSpan struct {
	code       Code
	start, end int
}

// scanner validates JSON one token at a time. It keeps its own stack instead
// of recursing, so nesting is bounded by MaxDepth and memory, never by the
// goroutine stack. Every consumer (Parse, Format, Repair, ParseTree, the JWT
// decoder) shares it, and with it one definition of valid JSON and one set of
// messages.
type scanner struct {
	data     []byte
	pos      int
	state    scanState
	stack    []frame
	lenient  bool
	maxDepth int
	comma    int    // offset of the latest comma, where a trailing-comma report points
	key      [2]int // span of the latest key, quoted in messages about it
	newline  bool   // a line break came before the current token
	err      *Error

	fixes          []fixSpan // lenient mode only
	wantSurrogates bool
	surrogates     []int // offsets of lone surrogate escapes, when wanted
}

var bom = []byte{0xEF, 0xBB, 0xBF}

func newScanner(data []byte, lenient bool, maxDepth int) *scanner {
	s := &scanner{data: data, lenient: lenient, maxDepth: maxDepth}
	switch {
	case bytes.HasPrefix(data, bom):
		if lenient {
			s.fixes = append(s.fixes, fixSpan{CodeBOM, 0, len(bom)})
			s.pos = len(bom)
		} else {
			s.fail(CodeBOM, 0, "the input starts with an invisible byte order mark (BOM), which JSON doesn't allow")
		}
	case looksUTF16(data):
		s.fail(CodeInvalidUTF8, 0, "the input looks like UTF-16 text; JSON must be saved as UTF-8")
	}
	return s
}

// looksUTF16 spots UTF-16 by its byte order mark or by the zero bytes that
// ASCII characters get in it. Valid JSON never has a zero byte, so this only
// improves the message for input that fails anyway.
func looksUTF16(d []byte) bool {
	if len(d) >= 2 && (d[0] == 0xFF && d[1] == 0xFE || d[0] == 0xFE && d[1] == 0xFF) {
		return true
	}
	return len(d) >= 4 && (d[0] != 0 && d[1] == 0 && d[3] == 0 || d[0] == 0 && d[1] != 0 && d[2] == 0)
}

// fail records the first error; later ones are consequences of it.
func (s *scanner) fail(code Code, off int, msg string) token {
	if s.err == nil {
		s.err = errorAt(code, off, msg)
	}
	return token{kind: tokError}
}

// error returns the recorded error with its position resolved.
func (s *scanner) error() *Error { return finish(s.data, s.err) }

// next returns the next bracket or literal, having checked that the grammar
// allows it there.
func (s *scanner) next() token {
	if s.err != nil {
		return token{kind: tokError}
	}
	s.newline = false
	for {
		s.skipSpace()
		if s.err != nil {
			return token{kind: tokError}
		}
		if s.pos >= len(s.data) {
			return s.atEnd()
		}
		c := s.data[s.pos]
		switch s.state {
		case stTop, stValue, stElemFirst, stElemNext:
			if c == ']' || c == '}' {
				return s.closeInValue(c)
			}
			return s.value(c)
		case stKeyFirst, stKeyNext:
			switch c {
			case '"':
				return s.keyToken()
			case '}', ']':
				if s.state == stKeyNext && !s.trailingComma() {
					return token{kind: tokError}
				}
				return s.close(c)
			}
			return s.badKey(c)
		case stColon:
			if c != ':' {
				return s.badColon(c)
			}
			s.pos++
			s.state = stValue
		case stAfter:
			switch c {
			case ',':
				s.comma = s.pos
				s.pos++
				if s.stack[len(s.stack)-1].obj {
					s.state = stKeyNext
				} else {
					s.state = stElemNext
				}
			case '}', ']':
				return s.close(c)
			default:
				return s.badAfter(c)
			}
		default:
			return s.extra(c)
		}
	}
}

// skipSpace skips JSON whitespace and, in lenient mode, comments.
func (s *scanner) skipSpace() {
	d := s.data
	for s.pos < len(d) {
		switch d[s.pos] {
		case ' ', '\t':
			s.pos++
		case '\n', '\r':
			s.newline = true
			s.pos++
		case '/':
			if !s.lenient || !s.skipComment() {
				return
			}
		default:
			return
		}
	}
}

func (s *scanner) skipComment() bool {
	d := s.data
	start := s.pos
	if start+1 >= len(d) {
		return false
	}
	switch d[start+1] {
	case '/':
		i := start + 2
		for i < len(d) && d[i] != '\n' && d[i] != '\r' {
			i++
		}
		s.fixes = append(s.fixes, fixSpan{CodeComment, start, i})
		s.pos = i
		return true
	case '*':
		k := bytes.Index(d[start+2:], []byte("*/"))
		if k < 0 {
			s.fail(CodeUnclosedComment, start, "this comment is never closed with */")
			return false
		}
		s.pos = start + 2 + k + 2
		s.fixes = append(s.fixes, fixSpan{CodeComment, start, s.pos})
		return true
	}
	return false
}

func (s *scanner) isCommentStart() bool {
	d := s.data
	return d[s.pos] == '/' && s.pos+1 < len(d) && (d[s.pos+1] == '/' || d[s.pos+1] == '*')
}

func (s *scanner) commentError() token {
	return s.fail(CodeComment, s.pos, "comments aren't allowed in JSON")
}

func (s *scanner) atEnd() token {
	switch s.state {
	case stDone:
		return token{kind: tokEOF, start: len(s.data), end: len(s.data)}
	case stTop:
		msg := "the input is empty"
		if len(s.data) > 0 {
			msg = "the input holds no JSON value, only spaces or line breaks"
			for _, f := range s.fixes {
				if f.code == CodeComment {
					msg = "the input holds no JSON value, only comments"
					break
				}
			}
		}
		return s.fail(CodeEmptyInput, 0, msg)
	}
	top := s.stack[len(s.stack)-1]
	what, want := "array", "]"
	if top.obj {
		what, want = "object", "}"
	}
	return s.fail(CodeUnexpectedEnd, len(s.data), "the input ends before the "+what+" that starts at "+s.where(top.open)+" is closed with "+want)
}

// where describes a second place for a message, as "line 2, column 9".
func (s *scanner) where(off int) string {
	p := locate(s.data, []int{off})[0]
	return "line " + strconv.Itoa(p.Line) + ", column " + strconv.Itoa(p.Column)
}

func (s *scanner) afterValue() {
	if len(s.stack) == 0 {
		s.state = stDone
	} else {
		s.state = stAfter
	}
}

func (s *scanner) push(obj bool) bool {
	if len(s.stack) >= s.maxDepth {
		s.fail(CodeTooDeep, s.pos, "the JSON is nested more than "+strconv.Itoa(s.maxDepth)+" levels deep")
		return false
	}
	s.stack = append(s.stack, frame{open: s.pos, obj: obj})
	s.pos++
	return true
}

func (s *scanner) close(c byte) token {
	top := s.stack[len(s.stack)-1]
	if top.obj != (c == '}') {
		want, what := "]", "array"
		if top.obj {
			want, what = "}", "object"
		}
		return s.fail(CodeMismatchedBracket, s.pos, "expected "+want+" to close the "+what+" that starts at "+s.where(top.open)+", but found "+string(c))
	}
	s.stack = s.stack[:len(s.stack)-1]
	start := s.pos
	s.pos++
	s.afterValue()
	if c == '}' {
		return token{kind: tokEndObject, start: start, end: s.pos}
	}
	return token{kind: tokEndArray, start: start, end: s.pos}
}

// closeInValue handles a closing bracket where a value could start.
func (s *scanner) closeInValue(c byte) token {
	switch s.state {
	case stElemFirst:
		return s.close(c)
	case stElemNext:
		if !s.trailingComma() {
			return token{kind: tokError}
		}
		return s.close(c)
	}
	return s.fail(CodeExpectedValue, s.pos, s.expectedValue()+", but found "+string(c))
}

// trailingComma handles a comma right before a closing bracket.
func (s *scanner) trailingComma() bool {
	if s.lenient {
		s.fixes = append(s.fixes, fixSpan{CodeTrailingComma, s.comma, s.comma + 1})
		return true
	}
	s.fail(CodeTrailingComma, s.comma, "remove this comma: JSON doesn't allow a comma after the last item")
	return false
}

func (s *scanner) expectedValue() string {
	switch s.state {
	case stValue:
		return "expected a value after the colon"
	case stElemFirst:
		return "expected a value or ]"
	case stElemNext:
		return "expected a value after the comma"
	}
	return "expected a value"
}

func (s *scanner) value(c byte) token {
	start := s.pos
	switch {
	case c == '{':
		if !s.push(true) {
			return token{kind: tokError}
		}
		s.state = stKeyFirst
		return token{kind: tokBeginObject, start: start, end: start + 1}
	case c == '[':
		if !s.push(false) {
			return token{kind: tokError}
		}
		s.state = stElemFirst
		return token{kind: tokBeginArray, start: start, end: start + 1}
	case c == '"':
		end, esc, ok := s.lexString(start)
		if !ok {
			return token{kind: tokError}
		}
		s.afterValue()
		return token{kind: tokString, start: start, end: end, escaped: esc}
	case c == '-' || isDigit(c):
		return s.number(start)
	case isWordStart(c):
		return s.word(start)
	}
	return s.badValue(c)
}

func (s *scanner) badValue(c byte) token {
	at := s.pos
	switch {
	case c == '\'':
		return s.fail(CodeSingleQuote, at, "text must be in double quotes (\"), not single quotes (')")
	case c == '/' && s.isCommentStart():
		return s.commentError()
	case c == '+':
		return s.fail(CodeInvalidNumber, at, "a number can't start with +; leave the + out")
	case c == '.':
		return s.fail(CodeInvalidNumber, at, "a number can't start with a decimal point; write 0.5, not .5")
	case c == ',' && s.state == stElemNext:
		return s.fail(CodeExpectedValue, at, "there are two commas in a row; remove one or put a value between them")
	case c >= utf8.RuneSelf:
		if t, ok := s.special(); ok {
			return t
		}
	}
	return s.fail(CodeExpectedValue, at, s.expectedValue()+", but found "+s.found())
}

// special reports the non-ASCII characters worth naming wherever they turn up
// between tokens.
func (s *scanner) special() (token, bool) {
	r, size := utf8.DecodeRune(s.data[s.pos:])
	switch {
	case r == utf8.RuneError && size == 1:
		return s.fail(CodeInvalidUTF8, s.pos, "this byte is not valid UTF-8 text"), true
	case isSmartQuote(r):
		return s.fail(CodeSmartQuote, s.pos, string(r)+" is a curly quote; JSON needs straight double quotes (\")"), true
	case spaceName(r) != "":
		return s.fail(CodeUnicodeSpace, s.pos, "this is "+spaceName(r)+"; JSON allows only ordinary spaces, tabs and line breaks between values"), true
	}
	return token{}, false
}

// found describes the character at the scan position for a message.
func (s *scanner) found() string {
	if s.pos >= len(s.data) {
		return "the end of the input"
	}
	r, _ := utf8.DecodeRune(s.data[s.pos:])
	return charName(r)
}

func (s *scanner) keyToken() token {
	start := s.pos
	end, esc, ok := s.lexString(start)
	if !ok {
		return token{kind: tokError}
	}
	s.key = [2]int{start, end}
	s.state = stColon
	return token{kind: tokKey, start: start, end: end, escaped: esc}
}

func (s *scanner) badKey(c byte) token {
	at := s.pos
	d := s.data
	switch {
	case c == '\'':
		return s.fail(CodeSingleQuote, at, "keys must be in double quotes (\"), not single quotes (')")
	case c == '/' && s.isCommentStart():
		return s.commentError()
	case c == ',' && s.state == stKeyNext:
		return s.fail(CodeExpectedKey, at, "there are two commas in a row; remove one")
	case isWordPart(c):
		end := at
		for end < len(d) && isWordPart(d[end]) {
			end++
		}
		if j := skipSpaces(d, end); j < len(d) && d[j] == ':' {
			return s.fail(CodeUnquotedKey, at, "keys must be in double quotes: write \""+clip(string(d[at:end]), 30)+"\"")
		}
	case c >= utf8.RuneSelf:
		if t, ok := s.special(); ok {
			return t
		}
	}
	return s.fail(CodeExpectedKey, at, "expected a key in double quotes, but found "+s.found())
}

func (s *scanner) badColon(c byte) token {
	if c >= utf8.RuneSelf {
		if t, ok := s.special(); ok {
			return t
		}
	}
	if c == '/' && s.isCommentStart() {
		return s.commentError()
	}
	k := clip(string(s.data[s.key[0]:s.key[1]]), 40)
	return s.fail(CodeExpectedColon, s.pos, "expected : after the key "+k+", but found "+s.found())
}

func (s *scanner) badAfter(c byte) token {
	if c >= utf8.RuneSelf {
		if t, ok := s.special(); ok {
			return t
		}
	}
	if c == '/' && s.isCommentStart() {
		return s.commentError()
	}
	msg := "expected , or ] after the value"
	if s.stack[len(s.stack)-1].obj {
		msg = "expected , or } after the value"
	}
	if startsValue(c) {
		msg += " (is a comma missing?)"
	} else {
		msg += ", but found " + s.found()
	}
	return s.fail(CodeExpectedCommaOrClose, s.pos, msg)
}

func (s *scanner) extra(c byte) token {
	at := s.pos
	if c >= utf8.RuneSelf {
		if t, ok := s.special(); ok {
			return t
		}
	}
	if c == '/' && s.isCommentStart() {
		return s.commentError()
	}
	switch {
	case c == '}' || c == ']':
		return s.fail(CodeExtraData, at, "this "+string(c)+" has nothing to close: the JSON value has already ended")
	case c == ',':
		return s.fail(CodeExtraData, at, "the JSON value has already ended; to hold several values, put them all inside [ and ]")
	case startsValue(c):
		msg := "a second value starts here, but JSON holds only one; to hold several, put them all inside [ and ]"
		if s.newline {
			msg += " (or, for one value per line, use a JSON Lines tool)"
		}
		return s.fail(CodeExtraData, at, msg)
	}
	return s.fail(CodeExtraData, at, "unexpected "+s.found()+" after the end of the JSON value")
}

// lexString checks the string literal starting at the quote at start and
// returns the offset just past its closing quote.
func (s *scanner) lexString(start int) (end int, escaped, ok bool) {
	d := s.data
	i := start + 1
	for {
		for i < len(d) {
			c := d[i]
			if c == '"' || c == '\\' || c < 0x20 || c >= utf8.RuneSelf {
				break
			}
			i++
		}
		if i >= len(d) {
			s.unterminated(start)
			return 0, false, false
		}
		switch c := d[i]; {
		case c == '"':
			s.pos = i + 1
			return i + 1, escaped, true
		case c == '\\':
			escaped = true
			n := s.escape(start, i)
			if n == 0 {
				return 0, false, false
			}
			i += n
		case c < 0x20:
			s.control(i, c)
			return 0, false, false
		default:
			r, size := utf8.DecodeRune(d[i:])
			if r == utf8.RuneError && size == 1 {
				s.fail(CodeInvalidUTF8, i, "this byte is not valid UTF-8 text")
				return 0, false, false
			}
			i += size
		}
	}
}

func (s *scanner) unterminated(start int) {
	s.fail(CodeUnterminatedString, start, "this string is never closed: a \" is missing at its end")
}

// escape checks the escape sequence at d[i] (a backslash) and returns its length.
func (s *scanner) escape(start, i int) int {
	d := s.data
	if i+1 >= len(d) {
		s.unterminated(start)
		return 0
	}
	switch d[i+1] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return 2
	case 'u':
		r, ok := hex4(d, i+2)
		if !ok {
			s.fail(CodeInvalidEscape, i, "\\u must be followed by four hexadecimal digits (0-9 and a-f), as in \\u00e9")
			return 0
		}
		if r < 0xD800 || r > 0xDFFF {
			return 6
		}
		if r < 0xDC00 {
			if _, ok := lowSurrogateAt(d, i+6); ok {
				return 12
			}
		}
		if s.wantSurrogates {
			s.surrogates = append(s.surrogates, i)
		}
		return 6
	}
	s.badEscape(i)
	return 0
}

func (s *scanner) badEscape(i int) {
	d := s.data
	c := d[i+1]
	var msg string
	switch {
	case c == 'x':
		msg = "\\x escapes aren't allowed in JSON; write \\u00 and the two hex digits instead"
		if i+4 <= len(d) && isHex(d[i+2]) && isHex(d[i+3]) {
			msg = "\\x escapes aren't allowed in JSON; write \\u00" + string(d[i+2:i+4]) + " instead"
		}
	case c == '\'':
		msg = "\\' isn't an escape in JSON; a single quote inside a string needs no backslash"
	case c == 'U':
		msg = "\\U isn't an escape in JSON; write a lowercase \\u followed by four hex digits"
	case c == '\n' || c == '\r':
		msg = "a backslash can't continue a string on the next line; write \\n for a line break"
	case isDigit(c):
		msg = "\\" + string(c) + " isn't an escape in JSON; write \\u followed by four hex digits, as in \\u0000"
	default:
		r, _ := utf8.DecodeRune(d[i+1:])
		msg = "\\" + charName(r) + " isn't an escape in JSON; a backslash may only be followed by \" \\ / b f n r t or u (write \\\\ for a backslash itself)"
	}
	s.fail(CodeInvalidEscape, i, msg)
}

func isHex(c byte) bool { return isDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

func (s *scanner) control(i int, c byte) {
	var msg string
	switch c {
	case '\n', '\r':
		msg = "a line break inside a string must be written as \\n; or is the closing \" missing?"
	case '\t':
		msg = "a tab inside a string must be written as \\t"
	default:
		msg = "the control character " + uPlus(rune(c)) + " must be written as \\u00" + string(hexDigits[c>>4]) + string(hexDigits[c&0xF]) + " inside a string"
	}
	s.fail(CodeControlCharacter, i, msg)
}

func (s *scanner) number(start int) token {
	d := s.data
	i := start
	if d[i] == '-' {
		i++
		if i >= len(d) || !isDigit(d[i]) {
			if bytes.HasPrefix(d[i:], []byte("Infinity")) {
				return s.fail(CodeNaNInfinity, start, "-Infinity is not allowed in JSON; use null, or a string such as \"-Infinity\"")
			}
			return s.fail(CodeInvalidNumber, start, "a minus sign must be followed by a digit")
		}
	}
	if d[i] == '0' {
		if i+1 < len(d) && isDigit(d[i+1]) {
			j := i
			for j < len(d) && isDigit(d[j]) {
				j++
			}
			lit := string(d[i:j])
			k := 0
			for k < len(lit)-1 && lit[k] == '0' {
				k++
			}
			return s.fail(CodeLeadingZero, i, "a number can't start with 0 unless it is 0: write "+lit[k:]+", not "+lit)
		}
		i++
	} else {
		for i < len(d) && isDigit(d[i]) {
			i++
		}
	}
	integer := true
	if i < len(d) && d[i] == '.' {
		integer = false
		dot := i
		i++
		if i >= len(d) || !isDigit(d[i]) {
			whole := string(d[start:dot])
			return s.fail(CodeInvalidNumber, dot, "a decimal point must be followed by a digit: write "+whole+".0 or "+whole+", not "+whole+".")
		}
		for i < len(d) && isDigit(d[i]) {
			i++
		}
	}
	if i < len(d) && (d[i] == 'e' || d[i] == 'E') {
		integer = false
		e := i
		i++
		if i < len(d) && (d[i] == '+' || d[i] == '-') {
			i++
		}
		if i >= len(d) || !isDigit(d[i]) {
			return s.fail(CodeInvalidNumber, e, "the exponent "+string(d[e])+" must be followed by digits, as in 1"+string(d[e])+"5")
		}
		for i < len(d) && isDigit(d[i]) {
			i++
		}
	}
	if i < len(d) && (isWordPart(d[i]) || d[i] == '.') {
		return s.badNumberTail(start, i)
	}
	s.pos = i
	s.afterValue()
	return token{kind: tokNumber, start: start, end: i, integer: integer}
}

// badNumberTail reports a number that runs into letters or another point,
// such as 0x1F, 1.2.3 or 12px.
func (s *scanner) badNumberTail(start, i int) token {
	d := s.data
	digits := start
	if d[digits] == '-' {
		digits++
	}
	if (d[i] == 'x' || d[i] == 'X') && i == digits+1 && d[digits] == '0' {
		return s.fail(CodeInvalidNumber, start, "hexadecimal numbers such as 0x1F aren't allowed in JSON; write the value in decimal")
	}
	j := i
	for j < len(d) && (isWordPart(d[j]) || d[j] == '.' || d[j] == '+' || d[j] == '-') {
		j++
	}
	return s.fail(CodeInvalidNumber, start, clip(string(d[start:j]), 30)+" is not a valid number")
}

// word reads true, false or null, and names the usual mistakes made in their
// place: other languages' spellings, NaN, and words that needed quotes.
func (s *scanner) word(start int) token {
	d := s.data
	i := start
	for i < len(d) && isWordPart(d[i]) {
		i++
	}
	var kind tokKind
	switch string(d[start:i]) {
	case "true":
		kind = tokTrue
	case "false":
		kind = tokFalse
	case "null":
		kind = tokNull
	default:
		return s.badWord(start, i)
	}
	s.pos = i
	s.afterValue()
	return token{kind: kind, start: start, end: i}
}

func (s *scanner) badWord(start, end int) token {
	w := string(s.data[start:end])
	lw := lowerASCII(w)
	switch {
	case w == "NaN" || w == "Infinity":
		return s.fail(CodeNaNInfinity, start, w+" is not allowed in JSON; use null, or a string such as \""+w+"\"")
	case w == "undefined":
		return s.fail(CodeInvalidLiteral, start, "undefined is not a JSON value; use null")
	case w == "None":
		return s.fail(CodeInvalidLiteral, start, "None is Python; JSON writes null")
	case lw == "true" || lw == "false" || lw == "null":
		return s.fail(CodeInvalidLiteral, start, "write "+lw+" in lowercase, not "+w)
	}
	for _, lit := range [...]string{"true", "false", "null"} {
		if len(w) < len(lit) && lit[:len(w)] == w {
			return s.fail(CodeInvalidLiteral, start, w+" is incomplete; did you mean "+lit+"?")
		}
	}
	// Quote the word up to the next delimiter, so "hello world" is shown whole.
	d := s.data
	j := end
	for j < len(d) && bytes.IndexByte([]byte(",:[]{}\"\n\r"), d[j]) < 0 {
		j++
	}
	shown := string(bytes.TrimRight(d[start:j], " \t"))
	return s.fail(CodeExpectedValue, start, "text must be in double quotes: write \""+clip(shown, 30)+"\"")
}
