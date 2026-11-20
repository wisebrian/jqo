package jsapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// call invokes a function the way the WebAssembly boundary does.
func call(t *testing.T, name string, args ...any) Reply {
	t.Helper()
	fn, ok := Funcs[name]
	if !ok {
		t.Fatalf("no function %q", name)
	}
	return fn(args)
}

// decode parses a reply's result, which must be valid JSON.
func decode(t *testing.T, r Reply, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.Result), v); err != nil {
		t.Fatalf("result %q: %v", r.Result, err)
	}
}

func TestParse(t *testing.T) {
	r := call(t, "parse", []byte(`{"a": 1, "a": 2}`))
	if r.Err != "" || r.HasOutput {
		t.Fatalf("got %+v", r)
	}
	var rep struct {
		Summary  struct{ Keys int }
		Warnings []struct{ Code string }
	}
	decode(t, r, &rep)
	if rep.Summary.Keys != 2 || len(rep.Warnings) != 1 || rep.Warnings[0].Code != "duplicate-key" {
		t.Errorf("report %s", r.Result)
	}

	r = call(t, "parse", []byte("[1 2]"))
	if !strings.HasPrefix(r.Err, "Line 1, column 4: ") {
		t.Fatalf("error %q", r.Err)
	}
	var e struct {
		Code string
		Pos  struct{ Line, Column int }
	}
	decode(t, r, &e)
	if e.Code != "expected-comma-or-close" || e.Pos.Column != 4 {
		t.Errorf("error result %s", r.Result)
	}

	r = call(t, "parse", []byte("[1, // c\n2,]"), map[string]any{"lenient": true})
	if r.Err != "" {
		t.Fatalf("lenient: %+v", r)
	}
}

func TestArgumentErrors(t *testing.T) {
	for _, tc := range []struct {
		args []any
		want string
	}{
		{nil, "parse: the input is missing"},
		{[]any{3.0}, "parse: the input must be a Uint8Array or a string"},
		{[]any{[]byte("1"), "x"}, "parse: the options must be an object"},
		{[]any{[]byte("1"), map[string]any{"lenent": true}}, `parse: unknown option "lenent"; the options are lenient`},
		{[]any{[]byte("1"), map[string]any{"lenient": "yes"}}, "parse: option lenient must be true or false"},
	} {
		r := call(t, "parse", tc.args...)
		if r.Err != tc.want || r.Result != "" {
			t.Errorf("parse(%v) = %+v, want error %q", tc.args, r, tc.want)
		}
	}
	if r := TooLarge(); !strings.Contains(r.Err, "4 MB") || !strings.Contains(r.Result, `"too-large"`) {
		t.Errorf("TooLarge() = %+v", r)
	}
}

func TestFormat(t *testing.T) {
	r := call(t, "format", []byte(`{"b": [1, 2], "a": "<x>"}`), map[string]any{"style": "minify", "sortKeys": true, "escapeHTML": true})
	if r.Err != "" || !r.HasOutput {
		t.Fatalf("got %+v", r)
	}
	if want := `{"a":"\u003cx\u003e","b":[1,2]}`; string(r.Output) != want {
		t.Errorf("output %s, want %s", r.Output, want)
	}
	r = call(t, "format", []byte("[1, // c\n2,]"), map[string]any{"lenient": true, "style": "tab"})
	var res struct {
		Fixes    []struct{ Code string }
		FixCount int
	}
	decode(t, r, &res)
	if string(r.Output) != "[\n\t1,\n\t2\n]" || res.FixCount != 2 || res.Fixes[1].Code != "trailing-comma" {
		t.Errorf("lenient: %q %s", r.Output, r.Result)
	}
	if r := call(t, "format", []byte("1"), map[string]any{"style": "pretty"}); r.Err != "format: option style must be one of indent2, indent4, tab, minify" {
		t.Errorf("bad style: %+v", r)
	}
	if r := call(t, "format", []byte("[]")); string(r.Output) != "[]" || r.Result != `{"fixes":[],"fixCount":0}` {
		t.Errorf("defaults: %+v", r)
	}
}

func TestCodecs(t *testing.T) {
	r := call(t, "decodeString", []byte(`"\"[1]\""`))
	if string(r.Output) != "[1]" || r.Result != `{"layers":2}` {
		t.Errorf("decodeString: %+v", r)
	}
	r = call(t, "decodeString", []byte(`"\"[1]\""`), map[string]any{"maxLayers": 1.0})
	if string(r.Output) != `"[1]"` {
		t.Errorf("decodeString maxLayers: %+v", r)
	}
	if r := call(t, "decodeBase64", []byte("eyJhIjoxfQ")); string(r.Output) != `{"a":1}` {
		t.Errorf("decodeBase64: %+v", r)
	}
	if r := call(t, "decodePercent", []byte("a+b"), map[string]any{"keepPlus": true}); string(r.Output) != "a+b" {
		t.Errorf("decodePercent: %+v", r)
	}
	if r := call(t, "encodeString", []byte("é<"), map[string]any{"escapeNonASCII": true, "escapeHTML": true}); !strings.HasPrefix(string(r.Output), `"`) || strings.ContainsAny(string(r.Output), "é<") {
		t.Errorf("encodeString: %+v", r)
	}
	if r := call(t, "encodeBase64", []byte{0xfb, 0xff}, map[string]any{"url": true, "noPadding": true}); string(r.Output) != "-_8" {
		t.Errorf("encodeBase64: %q", r.Output)
	}
	if r := call(t, "encodePercent", []byte("a b")); string(r.Output) != "a%20b" {
		t.Errorf("encodePercent: %q", r.Output)
	}
	r = call(t, "encodeJWT", []byte(`{"alg":"HS256"}`), []byte(`{"a":1}`), []byte("k"))
	if r.Err != "" || strings.Count(string(r.Output), ".") != 2 {
		t.Errorf("encodeJWT: %+v", r)
	}
	if r := call(t, "encodeJWT", []byte(`{"alg":"HS256"}`), []byte(`{"a":1}`)); r.Err != "HS256 needs a secret to sign with" {
		t.Errorf("encodeJWT without secret: %+v", r)
	}
	if r := call(t, "decodeBase64", []byte("x"), map[string]any{"url": true}); !strings.Contains(r.Err, "takes no options") {
		t.Errorf("decodeBase64 options: %+v", r)
	}
}

func TestDecodeJWTTime(t *testing.T) {
	enc := "eyJhbGciOiJub25lIn0.eyJleHAiOjEwMH0." // {"alg":"none"} . {"exp":100}
	var j struct {
		Expired          bool
		SignatureChecked bool
		Times            []struct{ Utc string }
	}
	r := call(t, "decodeJWT", []byte(enc), 99.9)
	decode(t, r, &j)
	if j.Expired || j.SignatureChecked || j.Times[0].Utc != "1970-01-01T00:01:40Z" {
		t.Errorf("before exp: %s", r.Result)
	}
	if r := call(t, "decodeJWT", []byte(enc)); !strings.Contains(r.Err, "time (seconds since 1970) is missing") {
		t.Errorf("no clock: %+v", r)
	}
	Now = func() int64 { return 100 }
	defer func() { Now = nil }()
	decode(t, call(t, "decodeJWT", []byte(enc)), &j)
	if !j.Expired {
		t.Errorf("at exp with the boundary clock: not expired")
	}
}

func TestDetect(t *testing.T) {
	var gs []struct{ Encoding, Confidence, Reason string }
	decode(t, call(t, "detect", []byte("eyJhIjoxfQ==")), &gs)
	if len(gs) != 1 || gs[0].Encoding != "base64" || gs[0].Confidence != "high" {
		t.Errorf("detect: %+v", gs)
	}
	if r := call(t, "detect", []byte("")); r.Result != "[]" {
		t.Errorf("detect nothing: %+v", r)
	}
}

func TestRepairAndTree(t *testing.T) {
	r := call(t, "repair", []byte("[1, // c\n2,]"))
	if string(r.Output) != "[1,\n2]" || !strings.Contains(r.Result, `"fixCount":2`) {
		t.Errorf("repair: %q %s", r.Output, r.Result)
	}
	var tree struct {
		Nodes []struct {
			Kind, Pointer string
			UTF16Start    int
		}
	}
	decode(t, call(t, "tree", []byte(`{"é": [true]}`)), &tree)
	if len(tree.Nodes) != 3 || tree.Nodes[2].Pointer != "/é/0" || tree.Nodes[2].Kind != "boolean" || tree.Nodes[2].UTF16Start != 7 {
		t.Errorf("tree: %+v", tree)
	}
	if r := call(t, "tree", []byte("[1,]")); !strings.Contains(r.Result, `"trailing-comma"`) {
		t.Errorf("tree strict: %+v", r)
	}
	if r := call(t, "tree", []byte("[1,]"), map[string]any{"lenient": true}); r.Err != "" {
		t.Errorf("tree lenient: %+v", r)
	}
}

// TestEveryFunctionRejectsBadArguments calls each exported function with
// the wrong kinds of argument: it must answer with an error, never panic.
func TestEveryFunctionRejectsBadArguments(t *testing.T) {
	for name, fn := range Funcs {
		for _, args := range [][]any{nil, {nil}, {1.0}, {true}, {map[string]any{}}, {[]byte("{}"), "x"}, {[]byte("{}"), map[string]any{"nope": 1.0}}} {
			if r := fn(args); r.Err == "" && r.Result == "" && !r.HasOutput {
				t.Errorf("%s(%v) returned nothing", name, args)
			}
		}
	}
}
