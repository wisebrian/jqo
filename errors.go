package jqo

import "strconv"

// Code identifies a kind of problem. Codes never change once released, so the
// UI can attach help text or translations to them; messages may be reworded.
type Code string

// Codes for input that is not valid JSON. Each comment shows a typical cause.
const (
	CodeEmptyInput           Code = "empty-input"             // nothing but spaces, line breaks or comments
	CodeBOM                  Code = "bom"                     // a byte order mark before the first value
	CodeInvalidUTF8          Code = "invalid-utf8"            // bytes that are not UTF-8, such as Latin-1 or UTF-16 text
	CodeComment              Code = "comment"                 // // or /* */ in strict mode
	CodeUnclosedComment      Code = "unclosed-comment"        // /* without */ in lenient mode
	CodeTrailingComma        Code = "trailing-comma"          // [1, 2,]
	CodeSingleQuote          Code = "single-quote"            // 'text'
	CodeSmartQuote           Code = "smart-quote"             // “text”, as word processors type it
	CodeUnquotedKey          Code = "unquoted-key"            // {name: 1}
	CodeNaNInfinity          Code = "nan-infinity"            // NaN, Infinity, -Infinity
	CodeLeadingZero          Code = "leading-zero"            // 007
	CodeInvalidNumber        Code = "invalid-number"          // 1.  .5  +1  0x1F  1e
	CodeInvalidLiteral       Code = "invalid-literal"         // True, NULL, undefined, None, tru
	CodeInvalidEscape        Code = "invalid-escape"          // \x41, \', \u12G4
	CodeControlCharacter     Code = "control-character"       // a raw line break or tab inside a string
	CodeUnterminatedString   Code = "unterminated-string"     // "text with no closing quote
	CodeUnicodeSpace         Code = "unicode-space"           // a no-break or zero-width space between values
	CodeExpectedValue        Code = "expected-value"          // {"a": } or an unquoted word as a value
	CodeExpectedKey          Code = "expected-key"            // {,} or {[1]: 2}
	CodeExpectedColon        Code = "expected-colon"          // {"a" 1}
	CodeExpectedCommaOrClose Code = "expected-comma-or-close" // [1 2], usually a missing comma
	CodeMismatchedBracket    Code = "mismatched-bracket"      // [1, 2}
	CodeUnexpectedEnd        Code = "unexpected-end"          // the input stops inside an object or array
	CodeExtraData            Code = "extra-data"              // a second value after the first
	CodeTooDeep              Code = "too-deep"                // nesting beyond Options.MaxDepth
	CodeTooLarge             Code = "too-large"               // input or output beyond its size limit
)

// Codes for warnings: the JSON is valid but probably not what was meant.
const (
	CodeDuplicateKey  Code = "duplicate-key"  // the same key twice in one object
	CodeUnsafeInteger Code = "unsafe-integer" // an integer beyond ±2^53, which JavaScript can't hold exactly
	CodeLoneSurrogate Code = "lone-surrogate" // \ud800 without its other half; an error in DecodeString
)

// Codes for the decoders and encoders.
const (
	CodeNotAString     Code = "not-a-string"    // DecodeString input is not a JSON string literal
	CodeInvalidBase64  Code = "invalid-base64"  // a character outside the alphabet, or an impossible length
	CodeInvalidPercent Code = "invalid-percent" // a % not followed by two hex digits
	CodeInvalidJWT     Code = "invalid-jwt"     // not three dot-separated parts, or a header EncodeJWT can't sign
)

// CodeInternal reports a bug in this package. Exported functions recover from
// their own panics and return this instead, because in WebAssembly a panic
// would stop the Go program and every later call with it.
const CodeInternal Code = "internal"

// Position locates a point in the input. Offset is exact; Line and Column are
// for people; UTF16 is for JavaScript.
type Position struct {
	// Offset counts bytes from the start of the input.
	Offset int
	// Line is 1-based. \n, \r\n and a lone \r each end a line, as in editors.
	Line int
	// Column is 1-based and counts characters (Unicode code points), not
	// bytes, because characters are what a person sees; a tab counts as one.
	Column int
	// UTF16 counts UTF-16 code units from the start, as JavaScript strings
	// and textarea selections do, so a page can select the spot directly
	// instead of re-encoding the text to find it.
	UTF16 int
}

// Error reports a problem with input. Every function in this package returns
// problems as *Error, so a UI can show all of them the same way.
type Error struct {
	Code Code // stable, for choosing help text or a translation
	// Message is plain language without the position, such as
	// "expected , or } after the value", so a UI can place it as it likes.
	Message string
	// Pos is zero (Line 0) when the problem has no single place, as with
	// CodeTooLarge.
	Pos Position
	// Snippet is the line holding Pos, cut to about 80 characters around it,
	// with control characters and invisible spaces replaced by visible
	// symbols so the problem can be seen. Caret is spaces (tabs where Snippet
	// has tabs, so both line up at any tab width) followed by ^, to print
	// under Snippet in a monospace font.
	Snippet, Caret string
}

// Error returns the message with its position, as in
// "Line 3, column 15: expected , or } after the value".
func (e *Error) Error() string {
	if e.Pos.Line == 0 {
		return capitalize(e.Message)
	}
	return "Line " + strconv.Itoa(e.Pos.Line) + ", column " + strconv.Itoa(e.Pos.Column) + ": " + e.Message
}

// errorAt builds an Error that knows only its offset until finish resolves it.
func errorAt(code Code, off int, msg string) *Error {
	return &Error{Code: code, Message: msg, Pos: Position{Offset: off}}
}

// finish turns e's offset into a line, column and snippet of data. Only the
// first error is ever resolved, so the extra pass over data is paid once.
func finish(data []byte, e *Error) *Error {
	e.Pos = locate(data, []int{e.Pos.Offset})[0]
	e.Snippet, e.Caret = snippet(data, e.Pos.Offset)
	return e
}

// tooLarge reports an input or output over its limit. It has no position:
// the problem is the whole, not a place in it.
func tooLarge(what string, size, limit int) *Error {
	return &Error{Code: CodeTooLarge, Message: what + " is " + sizeText(size) + "; the limit is " + sizeText(limit)}
}

// sizeText writes a byte count the way people read file sizes.
func sizeText(n int) string {
	const mb, kb = 1 << 20, 1 << 10
	switch {
	case n >= mb:
		return tenths(n*10/mb) + " MB"
	case n >= kb:
		return tenths(n*10/kb) + " KB"
	}
	return strconv.Itoa(n) + " bytes"
}

func tenths(t int) string {
	s := strconv.Itoa(t / 10)
	if t%10 != 0 {
		s += "." + strconv.Itoa(t%10)
	}
	return s
}

func capitalize(s string) string {
	if s != "" && 'a' <= s[0] && s[0] <= 'z' {
		return string(s[0]-'a'+'A') + s[1:]
	}
	return s
}
