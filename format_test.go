package jqo

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// formatVariants are the option sets the golden files cover.
var formatVariants = map[string]FormatOptions{
	"indent2": {},
	"indent4": {Style: Indent4},
	"tab":     {Style: IndentTab},
	"minify":  {Style: Minify},
	"sorted":  {SortKeys: true},
	"ascii":   {Escape: Escape{NonASCII: true}},
	"html":    {Style: Minify, Escape: Escape{HTML: true}},
}

var lenientVariants = map[string]FormatOptions{
	"lenient-indent2":       {Options: Options{Lenient: true}},
	"lenient-sorted-minify": {Options: Options{Lenient: true}, Style: Minify, SortKeys: true},
}

// TestFormatGolden compares Format with testdata/format/<input>.<variant>.golden.
// Run go test -run TestFormatGolden -update to rewrite them after a change.
func TestFormatGolden(t *testing.T) {
	inputs, err := filepath.Glob("testdata/format/*.json*")
	if err != nil || len(inputs) == 0 {
		t.Fatal("no golden inputs", err)
	}
	for _, in := range inputs {
		data, err := os.ReadFile(in)
		if err != nil {
			t.Fatal(err)
		}
		variants := formatVariants
		if strings.HasSuffix(in, ".jsonc") {
			variants = lenientVariants
		}
		base := strings.TrimSuffix(in, filepath.Ext(in))
		for name, opts := range variants {
			golden := base + "." + name + ".golden"
			res, err := Format(data, opts)
			if err != nil {
				t.Errorf("%s %s: %v", in, name, err)
				continue
			}
			if *update {
				if err := os.WriteFile(golden, res.Output, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Errorf("%s: %v (run with -update to create it)", golden, err)
				continue
			}
			if !bytes.Equal(res.Output, want) {
				t.Errorf("%s: got\n%s\nwant\n%s", golden, res.Output, want)
			}
		}
	}
}

func decodeUseNumber(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}

// checkFormatted verifies what any formatted output promises: it is valid,
// it means exactly what the input meant, and formatting it again changes
// nothing.
func checkFormatted(t testing.TB, data, out []byte, opts FormatOptions) {
	t.Helper()
	if !json.Valid(out) {
		t.Fatalf("output is not valid JSON:\n%s", out)
	}
	if _, err := Parse(out, Options{MaxSize: len(out) + 1}); err != nil {
		t.Fatalf("output rejected by Parse: %v\n%s", err, out)
	}
	before, err1 := decodeUseNumber(data)
	after, err2 := decodeUseNumber(out)
	if err1 != nil || err2 != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("meaning changed (%v, %v):\n%s\n->\n%s", err1, err2, data, out)
	}
	opts.Lenient = false
	opts.MaxSize = len(out) + 1
	again, err := Format(out, opts)
	if err != nil || !bytes.Equal(again.Output, out) {
		t.Fatalf("not idempotent (%v):\n%s\n->\n%s", err, out, again.Output)
	}
}

func formatCorpus(t *testing.T) [][]byte {
	var corpus [][]byte
	for _, s := range validSamples {
		corpus = append(corpus, []byte(s))
	}
	inputs, _ := filepath.Glob("testdata/format/*.json")
	for _, in := range inputs {
		data, err := os.ReadFile(in)
		if err != nil {
			t.Fatal(err)
		}
		corpus = append(corpus, data)
	}
	return append(corpus, doc(50000))
}

func TestFormatMatchesEncodingJSON(t *testing.T) {
	for _, data := range formatCorpus(t) {
		for name, opts := range formatVariants {
			res, err := Format(data, opts)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			checkFormatted(t, data, res.Output, opts)
		}
		// Without sorting or escapes, encoding/json's own token-preserving
		// Compact and Indent give exactly the expected output.
		var compact, indent bytes.Buffer
		if err := json.Compact(&compact, data); err != nil {
			t.Fatal(err)
		}
		if err := json.Indent(&indent, bytes.TrimRight(data, " \t\r\n"), "", "  "); err != nil {
			t.Fatal(err)
		}
		if res, _ := Format(data, FormatOptions{Style: Minify}); !bytes.Equal(res.Output, compact.Bytes()) {
			t.Errorf("Minify differs from json.Compact:\n%s\n%s", res.Output, compact.Bytes())
		}
		if res, _ := Format(data, FormatOptions{}); !bytes.Equal(res.Output, indent.Bytes()) {
			t.Errorf("Indent2 differs from json.Indent:\n%s\n%s", res.Output, indent.Bytes())
		}
		// Escape.HTML escapes the same characters as json.HTMLEscape.
		var html bytes.Buffer
		json.HTMLEscape(&html, compact.Bytes())
		if res, _ := Format(data, FormatOptions{Style: Minify, Escape: Escape{HTML: true}}); !bytes.Equal(res.Output, html.Bytes()) {
			t.Errorf("HTML escaping differs from json.HTMLEscape:\n%s\n%s", res.Output, html.Bytes())
		}
	}
}

func TestFormatPreservesLiterals(t *testing.T) {
	in := "{\"n\": 1.0, \"big\": 12345678901234567890, \"e\": 1E+05, \"s\": \"\\u00E9\\/<&>\"}"
	res, err := Format([]byte(in), FormatOptions{Style: Minify})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"n\":1.0,\"big\":12345678901234567890,\"e\":1E+05,\"s\":\"\\u00E9\\/<&>\"}"; string(res.Output) != want {
		t.Errorf("got %s, want %s", res.Output, want)
	}
}

func TestFormatErrorsMatchParse(t *testing.T) {
	for _, tc := range errorCases {
		_, perr := Parse([]byte(tc.in), Options{})
		for name, opts := range formatVariants {
			_, ferr := Format([]byte(tc.in), opts)
			p, f := asError(t, perr), asError(t, ferr)
			if p.Code != f.Code || p.Pos != f.Pos || p.Message != f.Message {
				t.Errorf("%s/%s: Format error %v differs from Parse error %v", tc.name, name, f, p)
			}
		}
	}
}

func TestFormatLimits(t *testing.T) {
	deep := []byte(strings.Repeat("[", DefaultMaxDepth) + strings.Repeat("]", DefaultMaxDepth))
	if _, err := Format(deep, FormatOptions{}); asError(t, err).Code != CodeTooLarge {
		t.Errorf("deep indent: %v", err)
	}
	if res, err := Format(deep, FormatOptions{Style: Minify}); err != nil || len(res.Output) != len(deep) {
		t.Errorf("deep minify: %v", err)
	}
	if _, err := Format([]byte("[1, 2, 3]"), FormatOptions{MaxOutput: 5}); asError(t, err).Code != CodeTooLarge {
		t.Errorf("small MaxOutput: %v", err)
	}
	_, err := Format([]byte("[1]"), FormatOptions{Options: Options{MaxSize: 2}})
	if e := asError(t, err); e.Code != CodeTooLarge || e.Pos.Line != 0 {
		t.Errorf("MaxSize: %v", e)
	}
	if res, err := Format([]byte(" true "), FormatOptions{}); err != nil || string(res.Output) != "true" {
		t.Errorf("scalar: %q %v", res.Output, err)
	}
}

func TestFormatLenient(t *testing.T) {
	in := "\xef\xbb\xbf[1, /* a */ 2, // b\n 3,]"
	if _, err := Format([]byte(in), FormatOptions{}); asError(t, err).Code != CodeBOM {
		t.Fatalf("strict: %v", err)
	}
	for _, sort := range []bool{false, true} {
		res, err := Format([]byte(in), FormatOptions{Options: Options{Lenient: true}, Style: Minify, SortKeys: sort})
		if err != nil || string(res.Output) != "[1,2,3]" || res.FixCount != 4 || len(res.Fixes) != 4 {
			t.Fatalf("sort=%v: %q %+v %v", sort, res.Output, res.Fixes, err)
		}
	}
}

// TestFormatMemory checks the size target: memory within a small multiple
// of the input, so a 4 MB document never needs hundreds of megabytes.
func TestFormatMemory(t *testing.T) {
	data := doc(4 << 20)
	for name, opts := range map[string]FormatOptions{"indent2": {}, "minify": {Style: Minify}, "sorted": {SortKeys: true}} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		opts.MaxSize = len(data)
		res, err := Format(data, opts)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		ratio := float64(after.TotalAlloc-before.TotalAlloc) / float64(len(data))
		t.Logf("%s: allocated %.1f× the input; output is %.1f× the input", name, ratio, float64(len(res.Output))/float64(len(data)))
		if ratio > 6 {
			t.Errorf("%s allocated %.1f× the input", name, ratio)
		}
	}
}

func FuzzFormat(f *testing.F) {
	for _, s := range validSamples {
		f.Add([]byte(s), uint8(0), false, false, false)
	}
	f.Add([]byte(`{"b":1,"a":{"d":[{"z":0,"y":1}],"c":2},"b":3}`), uint8(0), true, false, false)
	f.Add([]byte(`{"é":"<&>","😀":"\\u2028"}`), uint8(3), true, true, true)
	f.Add([]byte("[1, 2 3]"), uint8(1), false, false, false)
	f.Fuzz(func(t *testing.T, data []byte, style uint8, sortKeys, nonASCII, html bool) {
		opts := FormatOptions{Style: Style(style % 4), SortKeys: sortKeys, Escape: Escape{NonASCII: nonASCII, HTML: html}}
		res, err := Format(data, opts)
		_, perr := Parse(data, Options{})
		if perr != nil {
			if err == nil {
				t.Fatalf("Format accepted %q, which Parse rejects: %v", data, perr)
			}
			p, f := asError(t, perr), asError(t, err)
			if p.Code != f.Code || p.Pos != f.Pos {
				t.Fatalf("Format error %v differs from Parse error %v", f, p)
			}
			return
		}
		if err != nil {
			if asError(t, err).Code == CodeTooLarge {
				return
			}
			t.Fatalf("Format rejected valid %q: %v", data, err)
		}
		checkFormatted(t, data, res.Output, opts)
		if !sortKeys && !nonASCII && !html && opts.Style == Minify {
			var compact bytes.Buffer
			if json.Compact(&compact, data) == nil && !bytes.Equal(compact.Bytes(), res.Output) {
				t.Fatalf("Minify differs from json.Compact for %q", data)
			}
		}
	})
}

func benchFormat(b *testing.B, size int, opts FormatOptions) {
	data := doc(size)
	opts.MaxSize = len(data)
	opts.MaxOutput = 4 * len(data)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Format(data, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFormat4MB(b *testing.B)        { benchFormat(b, 4<<20, FormatOptions{}) }
func BenchmarkFormat20MB(b *testing.B)       { benchFormat(b, 20<<20, FormatOptions{}) }
func BenchmarkFormatMinify20MB(b *testing.B) { benchFormat(b, 20<<20, FormatOptions{Style: Minify}) }
func BenchmarkFormatSorted20MB(b *testing.B) { benchFormat(b, 20<<20, FormatOptions{SortKeys: true}) }
