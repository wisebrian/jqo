package jqo

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestParseTree(t *testing.T) {
	in := "{\"a\": [1.0, \"x\\ny\"], \"b/c~d\": {\"e\": null}, \"f\": true, \"k\\u0062\": 12345678901234567890}"
	tree, err := ParseTree([]byte(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Node{
		{Kind: Object, Pointer: "", Parent: -1, Len: 4},
		{Kind: Array, Pointer: "/a", Key: "a", Depth: 1, Len: 2},
		{Kind: Number, Pointer: "/a/0", Depth: 2, Parent: 1, Value: "1.0"},
		{Kind: String, Pointer: "/a/1", Index: 1, Depth: 2, Parent: 1, Value: "x\ny"},
		{Kind: Object, Pointer: "/b~1c~0d", Key: "b/c~d", Index: 1, Depth: 1, Len: 1},
		{Kind: Null, Pointer: "/b~1c~0d/e", Key: "e", Depth: 2, Parent: 4, Value: "null"},
		{Kind: Bool, Pointer: "/f", Key: "f", Index: 2, Depth: 1, Value: "true"},
		{Kind: Number, Pointer: "/kb", Key: "kb", Index: 3, Depth: 1, Value: "12345678901234567890"},
	}
	if len(tree.Nodes) != len(want) {
		t.Fatalf("%d nodes: %+v", len(tree.Nodes), tree.Nodes)
	}
	for i, w := range want {
		n := tree.Nodes[i]
		w.Start, w.End, w.UTF16Start, w.UTF16End = n.Start, n.End, n.UTF16Start, n.UTF16End
		if n != w {
			t.Errorf("node %d = %+v\nwant %+v", i, n, w)
		}
	}
	for i, lit := range map[int]string{0: in, 1: "[1.0, \"x\\ny\"]", 3: "\"x\\ny\"", 4: "{\"e\": null}", 7: "12345678901234567890"} {
		if got := in[tree.Nodes[i].Start:tree.Nodes[i].End]; got != lit {
			t.Errorf("node %d spans %q, want %q", i, got, lit)
		}
	}
}

func TestParseTreeUTF16(t *testing.T) {
	in := `{"é😀": ["日", 1]}`
	tree, err := ParseTree([]byte(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	units := func(s string) int { return len(utf16.Encode([]rune(s))) }
	for _, n := range tree.Nodes {
		if n.UTF16Start != units(in[:n.Start]) || n.UTF16End != units(in[:n.End]) {
			t.Errorf("node %s: UTF-16 span %d-%d, want %d-%d", n.Pointer, n.UTF16Start, n.UTF16End, units(in[:n.Start]), units(in[:n.End]))
		}
	}
}

func TestParseTreeLimits(t *testing.T) {
	_, err := ParseTree([]byte("[1, 2"), Options{})
	if e := checkError(t, []byte("[1, 2"), err); e.Code != CodeUnexpectedEnd {
		t.Errorf("invalid input: %v", e)
	}
	if _, err := ParseTree([]byte("[1, // c\n]"), Options{Lenient: true}); err != nil {
		t.Errorf("lenient: %v", err)
	}
	many := "[" + strings.Repeat("0,", MaxTreeNodes) + "0]"
	_, err = ParseTree([]byte(many), Options{})
	if e := checkError(t, []byte(many), err); e.Code != CodeTooLarge || e.Pos.Line == 0 {
		t.Errorf("too many nodes: %v", e)
	}
	// The node limit must not hide a syntax error further on.
	_, err = ParseTree([]byte(many[:len(many)-1]+",]"), Options{})
	if e := asError(t, err); e.Code != CodeTrailingComma {
		t.Errorf("syntax error after the limit: %v", e)
	}
	deep := strings.Repeat("[", DefaultMaxDepth) + strings.Repeat("]", DefaultMaxDepth)
	if _, err := ParseTree([]byte(deep), Options{}); asError(t, err).Code != CodeTooLarge {
		t.Errorf("deep pointers: %v", err)
	}
	big := make([]byte, DefaultMaxTreeSize+1)
	if _, err := ParseTree(big, Options{}); asError(t, err).Code != CodeTooLarge {
		t.Errorf("large input: %v", err)
	}
}

func TestTreeJSON(t *testing.T) {
	tree, _ := ParseTree([]byte(`{"k":"\ud800"}`), Options{})
	b, _ := tree.MarshalJSON()
	var got struct {
		Nodes []struct {
			Kind, Pointer, Key, Value string
			Parent, Len               int
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].Kind != "object" || got.Nodes[1].Kind != "string" || got.Nodes[1].Pointer != "/k" || got.Nodes[0].Parent != -1 {
		t.Errorf("got %s", b)
	}
	if !strings.Contains(string(b), `"value":"\ud800"`) {
		t.Errorf("lone surrogate not kept as an escape: %s", b)
	}
}

func FuzzTree(f *testing.F) {
	for _, s := range validSamples {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tree, err := ParseTree(data, Options{})
		rep, perr := Parse(data, Options{})
		if perr != nil {
			if err == nil {
				t.Fatalf("ParseTree accepted %q", data)
			}
			return
		}
		if err != nil {
			if asError(t, err).Code != CodeTooLarge {
				t.Fatalf("ParseTree(%q): %v", data, err)
			}
			return
		}
		s := rep.Summary
		if len(tree.Nodes) != s.Objects+s.Arrays+s.Strings+s.Numbers+s.Booleans+s.Nulls {
			t.Fatalf("%d nodes for %+v", len(tree.Nodes), s)
		}
		for i, n := range tree.Nodes {
			if n.Start < 0 || n.End > len(data) || n.Start >= n.End || n.Parent >= i || n.UTF16Start > n.Start || n.UTF16End < n.UTF16Start {
				t.Fatalf("node %d %+v in %q", i, n, data)
			}
			if i > 0 {
				p := tree.Nodes[n.Parent]
				if !strings.HasPrefix(n.Pointer, p.Pointer+"/") || n.Depth != p.Depth+1 || n.Start < p.Start || n.End > p.End {
					t.Fatalf("node %d %+v does not sit in its parent %+v", i, n, p)
				}
			}
		}
	})
}
