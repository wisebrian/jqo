package jqo

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMaxTreeSize is ParseTree's default input limit. It is lower
	// than DefaultMaxSize because every value becomes a Node that the page
	// holds and renders.
	DefaultMaxTreeSize = 2 << 20
	// MaxTreeNodes caps the nodes ParseTree returns. A view with more is
	// unusable anyway, and the nodes would crowd WebAssembly memory.
	MaxTreeNodes = 200000
	// maxPointerBytes caps the combined length of all JSON Pointers, which
	// grows with depth times the number of nodes: a few kilobytes of deeply
	// nested arrays would otherwise need hundreds of megabytes.
	maxPointerBytes = 32 << 20
)

// Kind is the JSON type of a node.
type Kind uint8

// The kinds of JSON value.
const (
	Null Kind = iota
	Bool
	Number
	String
	Array
	Object
)

// String returns the kind's name as JSON Schema spells it: "null",
// "boolean", "number", "string", "array" or "object".
func (k Kind) String() string {
	switch k {
	case Null:
		return "null"
	case Bool:
		return "boolean"
	case Number:
		return "number"
	case String:
		return "string"
	case Array:
		return "array"
	case Object:
		return "object"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Node is one value in a document.
type Node struct {
	Kind Kind // the JSON type
	// Pointer locates the value by RFC 6901, such as "/users/0/name"; the
	// root's is "". A UI can show it, copy it, or use it as a stable id.
	Pointer string
	Key     string // member name, decoded; "" for array elements and the root
	Index   int    // position among its siblings, from 0
	Depth   int    // 0 for the root
	Parent  int    // index of the parent in Tree.Nodes; -1 for the root
	// Value is a number literal exactly as written, the decoded text of a
	// string, or true, false or null; it is empty for objects and arrays.
	Value string
	Len   int // children of an object or array
	// Start and End are the byte span of the value in the input, End
	// exclusive. UTF16Start and UTF16End are the same span in UTF-16 code
	// units, which is how a page selects text in a textarea or an editor.
	Start, End           int // byte offsets
	UTF16Start, UTF16End int // UTF-16 offsets
}

// Tree is a document's nodes in document order, depth first, so a parent
// always comes before its children.
type Tree struct {
	Nodes []Node // depth first, each parent before its children
}

// ParseTree lists the nodes of data for a collapsible view. Invalid input
// fails with the same *Error as Parse. Its input limit defaults to
// DefaultMaxTreeSize, and a document with more than MaxTreeNodes values, or
// nesting so deep that its pointers would be enormous, fails with
// CodeTooLarge: Format still handles those.
func ParseTree(data []byte, opts Options) (tree *Tree, err error) {
	defer guard(&tree, &err)
	return parseTree(data, opts)
}

func parseTree(data []byte, opts Options) (*Tree, error) {
	if limit := opts.maxSize(DefaultMaxTreeSize); len(data) > limit {
		return nil, tooLarge("the input", len(data), limit)
	}
	s := newScanner(data, opts.Lenient, opts.maxDepth())
	var (
		nodes    []Node
		stack    []int // indices of the open containers
		key      string
		ptrBytes int
		limitErr *Error // kept until the scan shows the input is valid
	)
	add := func(kind Kind, t token) int {
		if limitErr != nil {
			return -1
		}
		if len(nodes) >= MaxTreeNodes {
			limitErr = errorAt(CodeTooLarge, t.start, "the tree view shows at most "+strconv.Itoa(MaxTreeNodes)+" values, and this document has more")
			return -1
		}
		n := Node{Kind: kind, Parent: -1, Start: t.start, End: t.end}
		if len(stack) > 0 {
			p := &nodes[stack[len(stack)-1]]
			n.Parent, n.Index, n.Depth = stack[len(stack)-1], p.Len, len(stack)
			p.Len++
			seg := strconv.Itoa(n.Index)
			if p.Kind == Object {
				n.Key, seg = key, key
			}
			n.Pointer = p.Pointer + "/" + escapePointer(seg)
			if ptrBytes += len(n.Pointer); ptrBytes > maxPointerBytes {
				limitErr = errorAt(CodeTooLarge, t.start, "the document is nested too deeply for the tree view")
				return -1
			}
		}
		nodes = append(nodes, n)
		return len(nodes) - 1
	}
	for {
		t := s.next()
		switch t.kind {
		case tokError:
			return nil, s.error()
		case tokEOF:
			if limitErr != nil {
				return nil, finish(data, limitErr)
			}
			setUTF16(data, nodes)
			return &Tree{Nodes: nodes}, nil
		case tokKey:
			if limitErr == nil {
				key = string(keyText(data, t))
			}
		case tokBeginObject, tokBeginArray:
			kind := Object
			if t.kind == tokBeginArray {
				kind = Array
			}
			stack = append(stack, add(kind, t))
		case tokEndObject, tokEndArray:
			if i := stack[len(stack)-1]; i >= 0 {
				nodes[i].End = t.end
			}
			stack = stack[:len(stack)-1]
		case tokString:
			if i := add(String, t); i >= 0 {
				nodes[i].Value = string(keyText(data, t))
			}
		case tokNumber, tokTrue, tokFalse, tokNull:
			kind := map[tokKind]Kind{tokNumber: Number, tokTrue: Bool, tokFalse: Bool, tokNull: Null}[t.kind]
			if i := add(kind, t); i >= 0 {
				nodes[i].Value = string(data[t.start:t.end])
			}
		}
	}
}

// escapePointer escapes a reference token as RFC 6901 requires: ~ as ~0
// and / as ~1, in that order so that the two can't be confused.
func escapePointer(seg string) string {
	if !strings.ContainsAny(seg, "~/") {
		return seg
	}
	return strings.ReplaceAll(strings.ReplaceAll(seg, "~", "~0"), "/", "~1")
}

// setUTF16 fills in the UTF-16 spans of all nodes in one pass over data.
func setUTF16(data []byte, nodes []Node) {
	offs := make([]int, 0, 2*len(nodes))
	for _, n := range nodes {
		offs = append(offs, n.Start, n.End)
	}
	order := make([]int32, len(offs))
	for i := range order {
		order[i] = int32(i)
	}
	slices.SortFunc(order, func(a, b int32) int { return cmp.Compare(offs[a], offs[b]) })
	at, u16 := 0, 0
	for _, i := range order {
		for at < offs[i] {
			r, size := utf8.DecodeRune(data[at:])
			at += size
			if r >= 0x10000 {
				u16 += 2
			} else {
				u16++
			}
		}
		if i%2 == 0 {
			nodes[i/2].UTF16Start = u16
		} else {
			nodes[i/2].UTF16End = u16
		}
	}
}

// MarshalJSON writes {"nodes":[...]}, each node as {"kind","pointer","key",
// "index","depth","parent","value","len","start","end","utf16Start",
// "utf16End"} with the kind as its name.
func (t *Tree) MarshalJSON() ([]byte, error) {
	b := append(make([]byte, 0, 64+128*len(t.Nodes)), `{"nodes":`...)
	b = appendList(b, len(t.Nodes), func(b []byte, i int) []byte {
		n := &t.Nodes[i]
		o := newObject(b)
		o.str("kind", n.Kind.String())
		o.str("pointer", n.Pointer)
		o.str("key", n.Key)
		o.int("index", n.Index)
		o.int("depth", n.Depth)
		o.int("parent", n.Parent)
		o.str("value", n.Value)
		o.int("len", n.Len)
		o.int("start", n.Start)
		o.int("end", n.End)
		o.int("utf16Start", n.UTF16Start)
		o.int("utf16End", n.UTF16End)
		return o.end()
	})
	return append(b, '}'), nil
}
