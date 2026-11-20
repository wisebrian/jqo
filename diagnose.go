package jqo

import (
	"bytes"
	"slices"
	"strings"
)

// A parser stops at the first byte that can't continue the document, and
// every JSON parser stops at the same byte: encoding/json and jsontext agree
// with this package. That byte is often not where the mistake is. A missing
// comma is noticed at the next key, a line further down; a missing } only
// where the text after it stops making sense inside the object it left open,
// at times the end of the input. People read the structure of a document
// from its indentation, so diagnose reads it too, and moves the error to
// where the comma or bracket most likely belongs, with the message saying
// where parsing stopped.
//
// It runs only once parsing has failed, so valid input never pays for it,
// and it leaves the error where it is when the indentation can't tell:
// minified JSON, contents not indented under their bracket, tabs where the
// lines around have spaces.

// fix is the edit a diagnosis proposes at its error's offset: delete del
// bytes there, then insert ins. The tests apply it to check the guess.
type fix struct {
	del int
	ins string
}

// maxLeave bounds how many open blocks one line is checked against, so that
// deep nesting can't make the replay quadratic. Indentation that deep tells
// a reader nothing anyway.
const maxLeave = 64

// diagnose returns e, the scanner's first error in data, moved to where the
// mistake most likely is, with the fix it proposes there; or e itself, and
// no fix, when the indentation says nothing for sure.
func diagnose(data []byte, lenient bool, maxDepth int, e *Error) (*Error, fix) {
	switch e.Code {
	case CodeExpectedCommaOrClose, CodeExpectedKey, CodeMismatchedBracket, CodeUnexpectedEnd, CodeExtraData:
	default:
		return e, fix{}
	}
	stop := e.Pos.Offset
	eof := stop >= len(data)
	r := newReplay(data, lenient, maxDepth)
	if !eof {
		// the byte where parsing stopped starts a token too, whose line
		// may have left a block
		r.follow(stop, data[stop] == '}' || data[stop] == ']')
	}
	if d, f, ok := r.unclosed(stop, eof); ok {
		return d, f
	}
	switch e.Code {
	case CodeExpectedCommaOrClose:
		if d, f, ok := r.missingComma(stop); ok {
			return d, f
		}
		if d, f, ok := r.missingBrace(stop); ok {
			return d, f
		}
	case CodeExtraData:
		if d, f, ok := r.extraBracket(stop); ok {
			return d, f
		}
	}
	return e, fix{}
}

// block is an object or array the replay has seen open.
type block struct {
	open   int // offset of its { or [
	obj    bool
	indent []byte // the leading space of the line it opens on
	// inner is 1 when the block's contents start on its own line or on a
	// line indented deeper than indent, so that a line back at indent has
	// left the block; -1 when they start on a line no deeper; 0 before them.
	inner int8
	// left is the first token that starts a line no deeper than indent
	// while the block is open: where its contents seem to end. cut is the
	// end of the token before left, where the closing bracket belongs. left
	// is -1 until such a line.
	left, cut int
	// closedBy is the bracket that closed the block from a line less
	// indented than its own; -1 if none did.
	closedBy int
}

func (b *block) closer() byte {
	if b.obj {
		return '}'
	}
	return ']'
}

func (b *block) name() string {
	if b.obj {
		return "object"
	}
	return "array"
}

// replay is a second scan of input that failed, following the indentation.
type replay struct {
	data   []byte
	stack  []block
	closed []block // blocks closed out of line after their contents ended, the ones with the earliest cut
	tail   []block // blocks closed out of line by the closing brackets since the latest other token
	root   block   // the outermost block, once closed

	seen      bool   // a token has been seen
	last      token  // the latest token
	lastOwn   bool   // it starts its line
	lastInd   []byte // and this is its indentation
	lineStart int    // start of the line holding the latest token
}

func newReplay(data []byte, lenient bool, maxDepth int) *replay {
	r := &replay{data: data}
	s := newScanner(data, lenient, maxDepth)
	for {
		t := s.next()
		if t.kind == tokError || t.kind == tokEOF {
			return r
		}
		r.add(t, t.kind == tokEndObject || t.kind == tokEndArray)
	}
}

// add follows one token; closer says it is a closing bracket.
func (r *replay) add(t token, closer bool) {
	ind, own := r.follow(t.start, closer)
	if !closer {
		r.tail = r.tail[:0]
	}
	switch t.kind {
	case tokBeginObject, tokBeginArray:
		r.stack = append(r.stack, block{open: t.start, obj: t.kind == tokBeginObject, indent: r.lineIndent(), left: -1, closedBy: -1})
	case tokEndObject, tokEndArray:
		b := r.stack[len(r.stack)-1]
		r.stack = r.stack[:len(r.stack)-1]
		switch {
		case len(r.stack) == 0:
			r.root = b
		// A bracket less indented than the block's own line belongs, by the
		// indentation, to a block further out, so the block's own bracket
		// is likely the one missing: where its contents ended, at a line
		// that fits the outer block, or else right before this bracket.
		// The second is only likely if the input ends with this bracket and
		// others like it; if more follows, the indentation is merely loose.
		case own && less(ind, b.indent) && b.left >= 0 && r.fits(b.left, &r.stack[len(r.stack)-1]):
			b.closedBy = t.start
			if b.left < t.start {
				r.closed = keep(r.closed, b)
			} else {
				r.tail = append(r.tail, b)
			}
		}
	}
	r.seen, r.last, r.lastOwn, r.lastInd = true, t, own, ind
}

// follow notes a token starting at off: whether it starts its line, how
// indented it is, whether it is the first content of the innermost block,
// and which open blocks its line has left.
func (r *replay) follow(off int, closer bool) (ind []byte, own bool) {
	ind, own = r.lineOf(off)
	if !closer && len(r.stack) > 0 {
		b := &r.stack[len(r.stack)-1]
		if b.inner == 0 {
			b.inner = -1
			if !own || less(b.indent, ind) {
				b.inner = 1
			}
		}
	}
	if own {
		r.leave(ind, closer, off)
	}
	return ind, own
}

// lineOf reports whether the token at off starts its line, with a line break
// since the token before and only space before it on the line, and that
// space: its indentation. A token on a new line moves lineStart there.
func (r *replay) lineOf(off int) (ind []byte, own bool) {
	from := 0
	if r.seen {
		from = r.last.end
	}
	i := off - 1
	for i >= from && r.data[i] != '\n' && r.data[i] != '\r' {
		i--
	}
	if i >= from {
		r.lineStart = i + 1
	} else if r.seen {
		return nil, false // on the line of the token before
	}
	sp := r.data[r.lineStart:off]
	for _, c := range sp {
		if c != ' ' && c != '\t' {
			return nil, false // a comment ends on this line before the token
		}
	}
	return sp, true
}

// lineIndent is the indentation of the line holding the latest token.
func (r *replay) lineIndent() []byte {
	i := r.lineStart
	for i < len(r.data) && (r.data[i] == ' ' || r.data[i] == '\t') {
		i++
	}
	return r.data[r.lineStart:i]
}

// leave notes a token that starts its line at indentation ind. The line has
// left every open block whose own line is indented at least as deep, or
// deeper for a closing bracket, as a bracket belongs to the block at its own
// indentation. Blocks further out are indented less, so the first block the
// line is still inside ends the search.
func (r *replay) leave(ind []byte, closer bool, at int) {
	for i, n := len(r.stack)-1, 0; i >= 0 && n < maxLeave; i, n = i-1, n+1 {
		b := &r.stack[i]
		if b.inner != 1 {
			return
		}
		if !less(ind, b.indent) && (closer || !bytes.Equal(ind, b.indent)) {
			return
		}
		if b.left < 0 {
			b.left, b.cut = at, r.last.end
		}
	}
}

// fits reports whether the token at off could continue parent once the
// block before it is closed: a key in an object, a value in an array, or
// parent's own closing bracket.
func (r *replay) fits(off int, parent *block) bool {
	c := r.data[off]
	switch {
	case c == '}' || c == ']':
		return c == parent.closer()
	case parent.obj:
		return r.keyAt(off)
	}
	return startsValue(c)
}

// keyAt reports whether a string starts at off and a colon follows it, as
// one does a key.
func (r *replay) keyAt(off int) bool {
	d := r.data
	if off >= len(d) || d[off] != '"' {
		return false
	}
	for i := off + 1; i < len(d); i++ {
		switch d[i] {
		case '\\':
			i++
		case '"':
			j := skipSpaces(d, i+1)
			return j < len(d) && d[j] == ':'
		case '\n', '\r':
			return false
		}
	}
	return false
}

// keep adds b to the blocks with the earliest cut, starting them over if b's
// cut is earlier.
func keep(blocks []block, b block) []block {
	switch {
	case len(blocks) == 0 || b.cut < blocks[0].cut:
		return append(blocks[:0], b)
	case b.cut == blocks[0].cut:
		return append(blocks, b)
	}
	return blocks
}

// unclosed finds the blocks most likely left without their closing bracket:
// the ones whose contents end, by the indentation, before the error. At the
// end of the input these are blocks still open, or closed by a bracket that
// belongs further out. Before it, they are blocks still open whose contents
// ended at the byte where parsing stopped or at the token before it, since
// that is what parsing could not take. The outermost block never counts:
// nothing may follow it.
func (r *replay) unclosed(stop int, eof bool) (*Error, fix, bool) {
	var cands []block
	if eof {
		cands = slices.Clone(r.closed)
		for _, b := range r.tail {
			cands = keep(cands, b)
		}
	}
	for i := len(r.stack) - 1; i >= 1; i-- {
		b := r.stack[i]
		if b.left < 0 || !eof && b.left != stop && b.left != r.last.start || !r.fits(b.left, &r.stack[i-1]) {
			continue
		}
		cands = keep(cands, b)
	}
	if len(cands) == 0 {
		return nil, fix{}, false
	}
	slices.SortFunc(cands, func(a, b block) int { return b.open - a.open }) // innermost first

	var names, closers []string
	closedBy := -1
	for _, b := range cands {
		names = append(names, "the "+b.name()+" that starts at "+place(r.data, b.open))
		closers = append(closers, string(b.closer()))
		if closedBy < 0 {
			closedBy = b.closedBy
		}
	}
	at, next := cands[0].cut, cands[0].left
	ins := strings.Join(closers, "")
	msg := strings.Join(names, " and ")
	if len(cands) == 1 {
		msg += " is not closed: a " + ins + " is missing here"
	} else {
		msg += " are not closed: " + ins + " are missing here"
	}
	// what follows the block needs a comma before it, unless it has one or
	// is a closing bracket
	if c := r.data[next]; c != '}' && c != ']' && bytes.IndexByte(r.data[at:next], ',') < 0 {
		ins += ","
		msg += ", and a comma after it"
	}
	switch {
	case closedBy >= 0:
		it := "it"
		if len(cands) > 1 {
			it = "the first"
		}
		msg += "; the " + string(r.data[closedBy]) + " at " + place(r.data, closedBy) + " closes " + it + " instead"
	case !eof:
		msg += "; parsing stopped at " + place(r.data, stop)
	}
	return errorAt(CodeMissingBracket, at, msg), fix{ins: ins}, true
}

// missingComma moves an error about a value that follows another without a
// comma, from the start of the second, on a later line, to the end of the
// first, where the comma goes.
func (r *replay) missingComma(stop int) (*Error, fix, bool) {
	if len(r.stack) == 0 || !r.seen {
		return nil, fix{}, false
	}
	top := &r.stack[len(r.stack)-1]
	what := "the value"
	switch c := r.data[stop]; {
	case top.obj && r.keyAt(stop):
		what = "the key"
	case top.obj || !startsValue(c) || c == '\'':
		return nil, fix{}, false
	}
	if bytes.IndexAny(r.data[r.last.end:stop], "\n\r") < 0 {
		return nil, fix{}, false // on one line, the error is already next to it
	}
	msg := "a comma is missing here, before " + what + " at " + place(r.data, stop)
	return errorAt(CodeExpectedCommaOrClose, r.last.end, msg), fix{ins: ","}, true
}

// missingBrace handles a string in an object followed by a colon where its
// value should end, as in "owner": "name": "mihai": the string reads like a
// key, so the object it opens has likely lost its {.
func (r *replay) missingBrace(stop int) (*Error, fix, bool) {
	if len(r.stack) == 0 || r.data[stop] != ':' || r.last.kind != tokString || !r.stack[len(r.stack)-1].obj {
		return nil, fix{}, false
	}
	k := clip(string(r.data[r.last.start:r.last.end]), 40)
	return errorAt(CodeMissingBracket, r.last.start, k+" is followed by a colon, like a key: is a { missing before it?"), fix{ins: "{"}, true
}

// extraBracket points at the bracket that ended the input's value when a
// key follows it: the key shows that the value was meant to go on, so the
// bracket is likely one too many. A bracket alone on its line at the value's
// own indentation ends it where it should, and is left alone.
func (r *replay) extraBracket(stop int) (*Error, fix, bool) {
	if k := r.last.kind; len(r.stack) != 0 || k != tokEndObject && k != tokEndArray {
		return nil, fix{}, false
	}
	i := stop
	if r.data[i] == ',' {
		i = skipSpaces(r.data, i+1)
	}
	if !r.keyAt(i) || r.lastOwn && bytes.Equal(r.lastInd, r.root.indent) {
		return nil, fix{}, false
	}
	c := string(r.data[r.last.start])
	msg := "this " + c + " closes the whole JSON value, but a key follows at " + place(r.data, i) + ": is the " + c + " one too many?"
	return errorAt(CodeExtraData, r.last.start, msg), fix{del: 1}, true
}

// less reports whether indentation a is shallower than b: a proper prefix of
// it. Indentation of tabs and spaces mixed differently is not comparable,
// and is never shallower.
func less(a, b []byte) bool { return len(a) < len(b) && bytes.HasPrefix(b, a) }
