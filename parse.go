package jqo

import (
	"bytes"
	"cmp"
	"slices"
	"strconv"
)

// Options are the settings Parse, Format, Repair and ParseTree share. The
// zero value means strict RFC 8259 with the default limits.
type Options struct {
	// Lenient accepts // and /* */ comments, trailing commas and a leading
	// byte order mark, and reports each as a Fix. Nothing else is relaxed:
	// guessing what a single quote or a NaN was meant to be could change data.
	Lenient bool
	// MaxDepth limits how deeply objects and arrays nest; 0 or less means
	// DefaultMaxDepth. The scanner keeps its own stack, so the limit protects
	// memory and the reader, not the Go stack.
	MaxDepth int
	// MaxSize limits the input, in bytes; 0 or less means DefaultMaxSize, or
	// DefaultMaxTreeSize for ParseTree.
	MaxSize int
}

const (
	// DefaultMaxDepth is also the nesting limit of encoding/json, so the two
	// agree on which documents are valid.
	DefaultMaxDepth = 10000
	// DefaultMaxSize is the largest input the browser tools take: beyond a
	// few megabytes a page stops feeling instant, and WebAssembly memory,
	// which never shrinks, holds several copies of a document while it works.
	DefaultMaxSize = 4 << 20
	// ListLimit caps how many warnings and fixes a result lists, so a
	// pathological input can't produce a report larger than itself; the
	// counts go on past it.
	ListLimit = 1000
)

func (o Options) maxDepth() int {
	if o.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return o.MaxDepth
}

func (o Options) maxSize(def int) int {
	if o.MaxSize <= 0 {
		return def
	}
	return o.MaxSize
}

// Summary describes what a valid document holds.
type Summary struct {
	Size     int // bytes of input
	MaxDepth int // deepest nesting of objects and arrays; 0 for a lone scalar
	// Objects through Nulls count the values of each kind; Strings leaves
	// out keys, which Keys counts.
	Objects, Arrays, Strings, Numbers, Booleans, Nulls int
	Keys                                               int // members of all objects together
}

// Warning flags JSON that is valid but probably not what its author meant.
type Warning struct {
	Code    Code     // CodeDuplicateKey, CodeUnsafeInteger or CodeLoneSurrogate
	Message string   // plain language, without the position
	Pos     Position // the key, number or escape the warning is about
	// First is where a duplicated key first appeared in its object, and zero
	// for the other codes. Keys are compared after decoding escapes, so "a"
	// and "\u0061" are the same key, as they are to every JSON parser.
	First Position
}

// Fix records one change lenient mode makes to turn the input into strict JSON.
type Fix struct {
	Code    Code     // CodeComment, CodeTrailingComma or CodeBOM
	Message string   // such as "removed a // comment"
	Pos     Position // start of the removed text
	End     Position // just past it
}

// Report is what Parse found in a valid document.
type Report struct {
	Summary Summary // what the document holds
	// Warnings are in input order, at most ListLimit of them; WarningCount
	// counts them all.
	Warnings     []Warning
	WarningCount int // all warnings, listed or not
	// Fixes lists, in lenient mode, what Repair would remove; FixCount
	// counts them all.
	Fixes    []Fix
	FixCount int // all fixes, listed or not
}

// Parse checks that data is JSON and reports warnings and a summary. For
// invalid input it returns an *Error for the first problem only: past it the
// input can't be read reliably, and later "errors" would mostly be echoes of
// the first one.
func Parse(data []byte, opts Options) (rep *Report, err error) {
	defer guard(&rep, &err)
	return parse(data, opts)
}

// note is a warning before its position is resolved.
type note struct {
	code       Code
	start, end int // the key, number or escape it is about
	first      int // CodeDuplicateKey: start of the earlier key
}

func parse(data []byte, opts Options) (*Report, error) {
	if limit := opts.maxSize(DefaultMaxSize); len(data) > limit {
		return nil, tooLarge("the input", len(data), limit)
	}
	s := newScanner(data, opts.Lenient, opts.maxDepth())
	s.wantSurrogates = true
	rep := &Report{}
	sum := &rep.Summary
	sum.Size = len(data)
	var notes []note
	var sets []keySet // one per open container, reused level by level
	for {
		t := s.next()
		switch t.kind {
		case tokError:
			return nil, s.error()
		case tokEOF:
			finishReport(data, rep, s, notes)
			return rep, nil
		case tokBeginObject, tokBeginArray:
			if t.kind == tokBeginObject {
				sum.Objects++
			} else {
				sum.Arrays++
			}
			d := len(s.stack)
			sum.MaxDepth = max(sum.MaxDepth, d)
			if d > len(sets) {
				sets = append(sets, keySet{})
			} else {
				sets[d-1].reset()
			}
		case tokKey:
			sum.Keys++
			if first, dup := sets[len(s.stack)-1].add(keyText(data, t), t.start); dup {
				notes = append(notes, note{code: CodeDuplicateKey, start: t.start, end: t.end, first: first})
			}
		case tokString:
			sum.Strings++
		case tokNumber:
			sum.Numbers++
			if t.integer && beyond53(data[t.start:t.end]) {
				notes = append(notes, note{code: CodeUnsafeInteger, start: t.start, end: t.end})
			}
		case tokTrue, tokFalse:
			sum.Booleans++
		case tokNull:
			sum.Nulls++
		}
	}
}

// keySet finds duplicate keys in one object. Most objects are small, so it
// compares names in place, as slices of the input, and only copies them into
// a map once an object grows past the point where scanning gets slow.
type keySet struct {
	keys []keyAt
	m    map[string]int
}

type keyAt struct {
	name []byte
	off  int
}

func (k *keySet) reset() {
	k.keys = k.keys[:0]
	k.m = nil
}

// add records a key and reports where an equal key appeared before.
func (k *keySet) add(name []byte, off int) (first int, dup bool) {
	if k.m != nil {
		if prev, ok := k.m[string(name)]; ok {
			return prev, true
		}
		k.m[string(name)] = off
		return 0, false
	}
	for _, e := range k.keys {
		if bytes.Equal(e.name, name) {
			return e.off, true
		}
	}
	k.keys = append(k.keys, keyAt{name, off})
	if len(k.keys) > 16 {
		k.m = make(map[string]int, 2*len(k.keys))
		for _, e := range k.keys {
			k.m[string(e.name)] = e.off
		}
	}
	return 0, false
}

// beyond53 reports whether an integer literal's magnitude exceeds 2^53, past
// which JavaScript numbers (IEEE 754 doubles) can't hold every integer.
func beyond53(lit []byte) bool {
	if lit[0] == '-' {
		lit = lit[1:]
	}
	const limit = "9007199254740992"
	if len(lit) != len(limit) {
		return len(lit) > len(limit)
	}
	return string(lit) > limit
}

// finishReport turns the scan's notes and fixes into positioned warnings and
// fixes, resolving every position in a single pass over data.
func finishReport(data []byte, rep *Report, s *scanner, notes []note) {
	for _, off := range s.surrogates {
		notes = append(notes, note{code: CodeLoneSurrogate, start: off, end: off + 6})
	}
	slices.SortStableFunc(notes, func(a, b note) int { return cmp.Compare(a.start, b.start) })
	rep.WarningCount = len(notes)
	notes = notes[:min(len(notes), ListLimit)]
	offs := make([]int, 0, 2*len(notes))
	for _, n := range notes {
		offs = append(offs, n.start)
		if n.code == CodeDuplicateKey {
			offs = append(offs, n.first)
		}
	}
	pos := locate(data, offs)

	rep.Warnings = make([]Warning, 0, len(notes))
	for _, n := range notes {
		w := Warning{Code: n.code, Pos: pos[0]}
		pos = pos[1:]
		lit := string(data[n.start:n.end])
		switch n.code {
		case CodeDuplicateKey:
			w.First = pos[0]
			pos = pos[1:]
			w.Message = "the key " + clip(lit, 40) + " appears more than once in this object; it first appears at line " +
				strconv.Itoa(w.First.Line) + ", column " + strconv.Itoa(w.First.Column)
		case CodeUnsafeInteger:
			w.Message = unsafeIntegerMessage(lit)
		case CodeLoneSurrogate:
			w.Message = lit + " is half of a surrogate pair; alone it is not a character, and most programs will turn it into �"
		}
		rep.Warnings = append(rep.Warnings, w)
	}
	rep.Fixes, rep.FixCount = resolveFixes(data, s.fixes)
}

// unsafeIntegerMessage says what JavaScript would make of the literal, which
// is what the reader needs to judge whether it matters.
func unsafeIntegerMessage(lit string) string {
	js := jsNumber(lit)
	shown := clip(lit, 40)
	if js == lit {
		return shown + " is beyond ±2^53, where JavaScript stops holding every integer exactly; this one survives, but nearby values would change"
	}
	return shown + " is too large for JavaScript to hold exactly (beyond ±2^53); it would read it as " + clip(js, 40)
}

// jsNumber renders a number literal the way JavaScript's Number would show
// it after JSON.parse.
func jsNumber(lit string) string {
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil && f == 0 {
		return lit
	}
	switch {
	case f > 1.7976931348623157e308:
		return "Infinity"
	case f < -1.7976931348623157e308:
		return "-Infinity"
	}
	return jsFormat(f)
}

// jsFormat follows ECMAScript's Number::toString: the shortest digits that
// round-trip, written out in full up to 21 integer digits.
func jsFormat(f float64) string {
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mant, exp := e, 0
	for i := 0; i < len(e); i++ {
		if e[i] == 'e' {
			mant = e[:i]
			exp, _ = strconv.Atoi(e[i+1:])
			break
		}
	}
	digits := make([]byte, 0, len(mant))
	for i := 0; i < len(mant); i++ {
		if mant[i] != '.' {
			digits = append(digits, mant[i])
		}
	}
	k, n := len(digits), exp+1
	var b []byte
	switch {
	case k <= n && n <= 21:
		b = append(digits, make([]byte, n-k)...)
		for i := k; i < n; i++ {
			b[i] = '0'
		}
	case 0 < n && n <= 21:
		b = append(append(append(b, digits[:n]...), '.'), digits[n:]...)
	case -6 < n && n <= 0:
		b = append(b, "0."...)
		for i := 0; i < -n; i++ {
			b = append(b, '0')
		}
		b = append(b, digits...)
	default:
		b = append(b, digits[0])
		if k > 1 {
			b = append(append(b, '.'), digits[1:]...)
		}
		b = append(b, 'e')
		if n-1 >= 0 {
			b = append(b, '+')
		}
		b = strconv.AppendInt(b, int64(n-1), 10)
	}
	return sign + string(b)
}

// fixList pairs lenient-mode fixes with their resolved start and end positions.
func fixList(data []byte, fixes []fixSpan, pos []Position) []Fix {
	out := make([]Fix, 0, len(fixes))
	for i, f := range fixes {
		var msg string
		switch {
		case f.code == CodeBOM:
			msg = "removed the byte order mark (BOM) at the start"
		case f.code == CodeTrailingComma:
			msg = "removed the comma after the last item"
		case data[f.start+1] == '/':
			msg = "removed a // comment"
		default:
			msg = "removed a /* */ comment"
		}
		out = append(out, Fix{Code: f.code, Message: msg, Pos: pos[2*i], End: pos[2*i+1]})
	}
	return out
}
