package jqo

import (
	"cmp"
	"slices"
)

// Repair makes lenient input strict by deleting comments, trailing commas
// and a leading byte order mark. Every other byte stays where it was, so the
// result differs from the input only where it had to. Spaces and tabs before
// a deleted comment go with it, and so does a line the comment had to
// itself, so no blank lines are left behind. Repair always works in lenient
// mode; opts supplies only the limits. Input that is invalid even leniently
// fails with the same *Error as Parse in lenient mode.
func Repair(data []byte, opts Options) (res *Result, err error) {
	defer guard(&res, &err)
	return repair(data, opts)
}

func repair(data []byte, opts Options) (*Result, error) {
	if limit := opts.maxSize(DefaultMaxSize); len(data) > limit {
		return nil, tooLarge("the input", len(data), limit)
	}
	s := newScanner(data, true, opts.maxDepth())
	for done := false; !done; {
		switch s.next().kind {
		case tokError:
			return nil, s.error()
		case tokEOF:
			done = true
		}
	}
	fixes := sortFixes(s.fixes)
	out := make([]byte, 0, len(data))
	prev := 0
	for _, f := range fixes {
		a, b := f.start, f.end
		if f.code != CodeComment {
			out = append(out, data[prev:a]...)
			prev = b
			continue
		}
		for a > prev && (data[a-1] == ' ' || data[a-1] == '\t') {
			a--
		}
		out = append(out, data[prev:a]...)
		e := b
		for e < len(data) && (data[e] == ' ' || data[e] == '\t') {
			e++
		}
		lineStart := len(out) == 0 || out[len(out)-1] == '\n' || out[len(out)-1] == '\r'
		switch {
		case e == len(data):
			b = e // nothing but spaces after it: they go too
		case !lineStart:
		case data[e] == '\n':
			b = e + 1
		case data[e] == '\r':
			b = e + 1
			if b < len(data) && data[b] == '\n' {
				b++
			}
		}
		prev = b
	}
	out = append(out, data[prev:]...)
	res := &Result{Output: out}
	res.Fixes, res.FixCount = resolveFixes(data, fixes)
	return res, nil
}

// sortFixes puts fixes in input order. The scanner finds a trailing comma
// only at the closing bracket, after any comment between the two.
func sortFixes(fixes []fixSpan) []fixSpan {
	return slices.SortedStableFunc(slices.Values(fixes), func(a, b fixSpan) int { return cmp.Compare(a.start, b.start) })
}
