package jqo

import (
	"bytes"
	"math"
	"slices"
	"unicode/utf8"
)

// Style is the layout Format writes.
type Style uint8

const (
	Indent2   Style = iota // two spaces per level: the zero value, as in most editors
	Indent4                // four spaces per level
	IndentTab              // one tab per level
	Minify                 // no whitespace at all
)

// Escape asks for characters inside strings to be written as \u escapes. The
// zero value escapes only what JSON requires: unlike encoding/json's Marshal,
// nothing is HTML-escaped unless asked, because that silently changes text.
type Escape struct {
	// NonASCII writes every character outside ASCII as \uXXXX, or as a
	// surrogate pair above U+FFFF, for channels that mangle UTF-8.
	NonASCII bool
	// HTML writes < > & U+2028 and U+2029 as escapes, so the text can sit
	// inside an HTML <script> element: < and > can't end the element, &
	// can't start an entity, and the two separators can't end a JavaScript
	// line, as they could before ES2019. encoding/json's HTMLEscape escapes
	// the same five.
	HTML bool
}

// FormatOptions configure Format.
type FormatOptions struct {
	Options
	Style  Style  // indentation, or none at all
	Escape Escape // characters to write as escapes inside strings
	// SortKeys orders the members of every object by the code points of
	// their decoded keys. The sort is stable, so duplicate keys all survive,
	// side by side and in their original order: dropping one would delete
	// data without a trace, and Parse already warns about them.
	SortKeys bool
	// MaxOutput limits the formatted output, in bytes; 0 or less means
	// DefaultMaxOutput. It is separate from MaxSize because pretty-printing
	// legitimately makes a document larger, while deep nesting could make it
	// explode: indentation grows with depth, so 20 KB of nested brackets
	// would pretty-print to about 200 MB.
	MaxOutput int
}

// DefaultMaxOutput leaves room to pretty-print a DefaultMaxSize input, which
// typically grows two or three times.
const DefaultMaxOutput = 32 << 20

// Result is the output of Format and Repair.
type Result struct {
	Output []byte // the formatted or repaired document
	// Fixes lists, in lenient mode, what was removed, in input order and at
	// most ListLimit of them; FixCount counts them all.
	Fixes    []Fix
	FixCount int // all fixes, listed or not
}

// MarshalJSON writes {"fixes","fixCount"}. Output is left out: it is the
// document itself, which the WebAssembly bridge passes as bytes.
func (r *Result) MarshalJSON() ([]byte, error) {
	o := newObject(nil)
	o.raw("fixes", appendFixes(nil, r.Fixes))
	o.int("fixCount", r.FixCount)
	return o.end(), nil
}

// Format lays data out again without re-encoding it. Key order (unless
// SortKeys), number literals and string literals are copied byte for byte,
// apart from what Escape asks for, so 1.0 stays 1.0 and 12345678901234567890
// stays exact; decoding into map[string]any and float64 would lose both.
// Format streams and builds no tree; SortKeys adds only an index of where
// each container ends, so sorting doesn't rescan nested values. Invalid
// input fails with the same *Error as Parse. The output has no final
// newline, like JSON.stringify's.
func Format(data []byte, opts FormatOptions) (res *Result, err error) {
	defer guard(&res, &err)
	return format(data, opts)
}

func format(data []byte, opts FormatOptions) (*Result, error) {
	if limit := opts.maxSize(DefaultMaxSize); len(data) > limit {
		return nil, tooLarge("the input", len(data), limit)
	}
	s := newScanner(data, opts.Lenient, opts.maxDepth())
	w := newWriter(opts, len(data))
	if opts.SortKeys {
		return formatSorted(data, s, w)
	}
	for {
		t := s.next()
		switch t.kind {
		case tokError:
			return nil, s.error()
		case tokEOF:
			return w.result(data, s.fixes)
		case tokBeginObject:
			w.open('{')
		case tokBeginArray:
			w.open('[')
		case tokEndObject:
			w.close('}')
		case tokEndArray:
			w.close(']')
		case tokKey:
			w.key(data[t.start:t.end])
		case tokString:
			w.str(data[t.start:t.end])
		default:
			w.literal(data[t.start:t.end])
		}
		if w.over {
			// Keep validating without writing: invalid input must fail with
			// the same error as Parse, even when its output would also be
			// too large.
			for {
				switch s.next().kind {
				case tokError:
					return nil, s.error()
				case tokEOF:
					return nil, w.tooLarge()
				}
			}
		}
	}
}

// writer lays tokens out. It decides commas and indentation from its own
// count of what each open container holds so far.
type writer struct {
	out      []byte
	unit     []byte // one level of indentation; nil when minifying
	pad      []byte // unit repeated, grown as nesting deepens
	esc      Escape
	counts   []int // values written in each open container
	afterKey bool
	max      int
	over     bool
}

func newWriter(opts FormatOptions, inputLen int) *writer {
	w := &writer{esc: opts.Escape, max: opts.MaxOutput}
	if w.max <= 0 {
		w.max = DefaultMaxOutput
	}
	switch opts.Style {
	case Minify:
	case Indent4:
		w.unit = []byte("    ")
	case IndentTab:
		w.unit = []byte("\t")
	default:
		w.unit = []byte("  ")
	}
	size := inputLen
	if w.unit != nil {
		size += size / 2
	}
	w.out = make([]byte, 0, min(size, w.max)+16)
	return w
}

func (w *writer) beforeValue() {
	if w.afterKey {
		w.afterKey = false
		return
	}
	n := len(w.counts)
	if n == 0 {
		return
	}
	if w.counts[n-1] > 0 {
		w.out = append(w.out, ',')
	}
	w.counts[n-1]++
	w.newline(n)
}

// newline starts a line at the given depth. Indentation is where output
// outgrows input, so this is where the limit is checked.
func (w *writer) newline(depth int) {
	if w.unit == nil {
		return
	}
	need := depth * len(w.unit)
	for len(w.pad) < need {
		w.pad = append(w.pad, w.unit...)
	}
	w.out = append(w.out, '\n')
	w.out = append(w.out, w.pad[:need]...)
	if len(w.out) > w.max {
		w.over = true
	}
}

func (w *writer) open(c byte) {
	w.beforeValue()
	w.out = append(w.out, c)
	w.counts = append(w.counts, 0)
}

func (w *writer) close(c byte) {
	n := len(w.counts)
	if w.counts[n-1] > 0 {
		w.newline(n - 1)
	}
	w.counts = w.counts[:n-1]
	w.out = append(w.out, c)
}

func (w *writer) key(lit []byte) {
	w.beforeValue()
	w.string(lit)
	w.out = append(w.out, ':')
	if w.unit != nil {
		w.out = append(w.out, ' ')
	}
	w.afterKey = true
}

func (w *writer) str(lit []byte) {
	w.beforeValue()
	w.string(lit)
}

func (w *writer) string(lit []byte) {
	if w.esc == (Escape{}) {
		w.out = append(w.out, lit...)
		return
	}
	w.out = appendEscapedLiteral(w.out, lit, w.esc)
}

func (w *writer) literal(lit []byte) {
	w.beforeValue()
	w.out = append(w.out, lit...)
}

func (w *writer) tooLarge() *Error {
	return &Error{Code: CodeTooLarge, Message: "the formatted output would be larger than " + sizeText(w.max)}
}

func (w *writer) result(data []byte, fixes []fixSpan) (*Result, error) {
	if len(w.out) > w.max {
		return nil, w.tooLarge()
	}
	res := &Result{Output: w.out}
	res.Fixes, res.FixCount = resolveFixes(data, fixes)
	return res, nil
}

// resolveFixes positions the first ListLimit fixes, in input order.
func resolveFixes(data []byte, all []fixSpan) ([]Fix, int) {
	fixes := sortFixes(all)
	fixes = fixes[:min(len(fixes), ListLimit)]
	offs := make([]int, 0, 2*len(fixes))
	for _, f := range fixes {
		offs = append(offs, f.start, f.end)
	}
	return fixList(data, fixes, locate(data, offs)), len(all)
}

// appendEscapedLiteral copies a string literal, writing the characters esc
// asks for as escapes. Escapes already in the literal are kept as written.
func appendEscapedLiteral(dst, lit []byte, esc Escape) []byte {
	start := 0
	for i := 0; i < len(lit); {
		c := lit[i]
		if c == '\\' {
			i += 2 // the escaped character is always ASCII
			continue
		}
		if c < utf8.RuneSelf {
			if esc.HTML && (c == '<' || c == '>' || c == '&') {
				dst = append(dst, lit[start:i]...)
				dst = appendEscape(dst, rune(c))
				start = i + 1
			}
			i++
			continue
		}
		r, size := utf8.DecodeRune(lit[i:])
		if esc.NonASCII || esc.HTML && (r == 0x2028 || r == 0x2029) {
			dst = append(dst, lit[start:i]...)
			dst = appendEscape(dst, r)
			start = i + size
		}
		i += size
	}
	return append(dst, lit[start:]...)
}

// formatSorted formats with sorted keys in two passes. The first validates
// and records where every container ends. The second walks the now-trusted
// input, gathering each object's members (skipping nested values through
// the index instead of rescanning them), sorts them and writes them out.
func formatSorted(data []byte, s *scanner, w *writer) (*Result, error) {
	if len(data) > math.MaxInt32 {
		return nil, tooLarge("the input", len(data), math.MaxInt32)
	}
	var starts, ends []int32
	var open []int
scan:
	for {
		t := s.next()
		switch t.kind {
		case tokError:
			return nil, s.error()
		case tokEOF:
			break scan
		case tokBeginObject, tokBeginArray:
			open = append(open, len(starts))
			starts = append(starts, int32(t.start))
			ends = append(ends, 0)
		case tokEndObject, tokEndArray:
			ends[open[len(open)-1]] = int32(t.start)
			open = open[:len(open)-1]
		}
	}
	p := sorter{data: data, starts: starts, ends: ends, lenient: s.lenient}
	if !p.run(w) {
		return nil, w.tooLarge()
	}
	return w.result(data, s.fixes)
}

// sorter walks input that the scanner has already validated.
type sorter struct {
	data    []byte
	starts  []int32 // offsets of every { and [, in input order
	ends    []int32 // offsets of the matching } and ]
	lenient bool
	// bufs holds a member buffer per depth. An object's members are needed
	// only while it is open, and no other object at its depth can open
	// before it closes, so the buffers can be reused instead of allocating
	// a slice per object.
	bufs [][]member
}

type member struct {
	key              []byte // decoded, for comparing
	keyStart, keyEnd int    // the key literal, for copying
	val              int    // where the value starts
}

type sortFrame struct {
	obj     bool
	members []member // objects: sorted members
	next    int      // objects: next member; arrays: offset to scan on from
}

func (p *sorter) run(w *writer) bool {
	d := p.data
	var stack []sortFrame
	emit := func(v int) {
		switch d[v] {
		case '{':
			w.open('{')
			stack = append(stack, sortFrame{obj: true, members: p.members(v, len(stack))})
		case '[':
			w.open('[')
			stack = append(stack, sortFrame{next: v + 1})
		case '"':
			w.str(d[v:p.end(v)])
		default:
			w.literal(d[v:p.end(v)])
		}
	}
	emit(p.skip(0))
	for len(stack) > 0 && !w.over {
		f := &stack[len(stack)-1]
		if f.obj {
			if f.next == len(f.members) {
				w.close('}')
				stack = stack[:len(stack)-1]
				continue
			}
			m := f.members[f.next]
			f.next++
			w.key(d[m.keyStart:m.keyEnd])
			emit(m.val)
			continue
		}
		i := p.skip(f.next)
		if d[i] == ',' {
			i = p.skip(i + 1)
		}
		if d[i] == ']' {
			w.close(']')
			stack = stack[:len(stack)-1]
			continue
		}
		f.next = p.end(i)
		emit(i)
	}
	return !w.over
}

// members gathers and sorts the members of the object at obj, which is
// open at the given depth.
func (p *sorter) members(obj, depth int) []member {
	d := p.data
	for len(p.bufs) <= depth {
		p.bufs = append(p.bufs, nil)
	}
	ms := p.bufs[depth][:0]
	i := p.skip(obj + 1)
	for d[i] != '}' {
		ks, ke := i, p.end(i)
		i = p.skip(p.skip(ke) + 1) // past the colon
		v := i
		key := d[ks+1 : ke-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			key, _ = appendUnquoted(nil, key)
		}
		ms = append(ms, member{key: key, keyStart: ks, keyEnd: ke, val: v})
		i = p.skip(p.end(v))
		if d[i] == ',' {
			i = p.skip(i + 1)
		}
	}
	// Byte order of UTF-8 is code point order; WTF-8 keeps lone surrogates
	// between U+D7FF and U+E000, where they belong.
	slices.SortStableFunc(ms, func(a, b member) int { return bytes.Compare(a.key, b.key) })
	p.bufs[depth] = ms
	return ms
}

// skip moves past whitespace and, in lenient mode, comments and a BOM.
func (p *sorter) skip(i int) int {
	d := p.data
	if i == 0 && p.lenient && bytes.HasPrefix(d, bom) {
		i = len(bom)
	}
	for i < len(d) {
		switch d[i] {
		case ' ', '\t', '\n', '\r':
			i++
		case '/':
			if d[i+1] == '/' {
				for i < len(d) && d[i] != '\n' && d[i] != '\r' {
					i++
				}
			} else {
				i += 2 + bytes.Index(d[i+2:], []byte("*/")) + 2
			}
		default:
			return i
		}
	}
	return i
}

// end returns the offset just past the value that starts at i.
func (p *sorter) end(i int) int {
	d := p.data
	switch d[i] {
	case '{', '[':
		k, _ := slices.BinarySearch(p.starts, int32(i))
		return int(p.ends[k]) + 1
	case '"':
		j := i + 1
		for d[j] != '"' {
			if d[j] == '\\' {
				j++
			}
			j++
		}
		return j + 1
	}
	j := i
	for j < len(d) && !isSpace(d[j]) && d[j] != ',' && d[j] != ']' && d[j] != '}' && d[j] != '/' {
		j++
	}
	return j
}
