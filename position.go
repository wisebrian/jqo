package jqo

import (
	"cmp"
	"slices"
	"unicode/utf8"
)

// locate converts byte offsets into positions in one pass over data, so a
// report with a thousand warnings costs no more than one with a single error.
// Computing each position from the start would be quadratic.
func locate(data []byte, offs []int) []Position {
	pos := make([]Position, len(offs))
	if len(offs) == 0 {
		return pos
	}
	order := make([]int, len(offs))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return cmp.Compare(offs[a], offs[b]) })
	c := cursor{line: 1, col: 1}
	for _, i := range order {
		c.advance(data, offs[i])
		pos[i] = Position{Offset: offs[i], Line: c.line, Column: c.col, UTF16: c.u16}
	}
	return pos
}

// cursor is a position being moved forward through the input.
type cursor struct{ off, line, col, u16 int }

func (c *cursor) advance(data []byte, to int) {
	if to > len(data) {
		to = len(data)
	}
	i := c.off
	for i < to {
		b := data[i]
		if b < utf8.RuneSelf {
			i++
			c.u16++
			// \r\n is one line break, counted at the \n.
			if b == '\n' || b == '\r' && (i >= len(data) || data[i] != '\n') {
				c.line++
				c.col = 1
			} else {
				c.col++
			}
			continue
		}
		r, size := utf8.DecodeRune(data[i:])
		if i+size > to {
			break // to falls inside a character: report the character's start
		}
		i += size
		c.col++
		if r >= 0x10000 {
			c.u16 += 2
		} else {
			c.u16++ // an invalid byte becomes one U+FFFD in a JavaScript string
		}
	}
	c.off = i
}

// snippetSide is how many characters a snippet keeps on each side of the
// problem, so that minified JSON on a single long line still gives a readable
// excerpt.
const snippetSide = 40

// snippet renders the line around off, and a caret line pointing at off.
func snippet(data []byte, off int) (text, caret string) {
	if off > len(data) {
		off = len(data)
	}
	start := off
	for start > 0 && data[start-1] != '\n' && data[start-1] != '\r' {
		start--
	}
	end := off
	for end < len(data) && data[end] != '\n' && data[end] != '\r' {
		end++
	}
	from := off
	for n := 0; n < snippetSide && from > start; n++ {
		_, size := utf8.DecodeLastRune(data[start:from])
		from -= size
	}
	to := off
	for n := 0; n < snippetSide && to < end; n++ {
		_, size := utf8.DecodeRune(data[to:end])
		to += size
	}
	var t, c []byte
	if from > start {
		t = append(t, "…"...)
		c = append(c, ' ')
	}
	for i := from; i < to; {
		r, size := utf8.DecodeRune(data[i:to])
		if i < off {
			if r == '\t' {
				c = append(c, '\t')
			} else {
				c = append(c, ' ')
			}
		}
		t = appendVisible(t, r, size)
		i += size
	}
	if to < end {
		t = append(t, "…"...)
	}
	c = append(c, '^')
	return string(t), string(c)
}

// appendVisible writes r so that it can be seen: the problem is often an
// invisible character, and a caret under nothing explains nothing.
func appendVisible(b []byte, r rune, size int) []byte {
	switch {
	case r == utf8.RuneError && size == 1, r >= 0x80 && r < 0xA0:
		return utf8.AppendRune(b, utf8.RuneError)
	case r == '\t':
		return append(b, '\t')
	case r < 0x20:
		return utf8.AppendRune(b, 0x2400+r) // Control Pictures: ␀ ␁ … ␟
	case r == 0x7F:
		return utf8.AppendRune(b, 0x2421) // ␡
	case spaceName(r) != "":
		return utf8.AppendRune(b, 0x2423) // ␣
	}
	return utf8.AppendRune(b, r)
}
