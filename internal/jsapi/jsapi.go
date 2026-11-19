// Package jsapi is the JavaScript-facing layer of the WebAssembly build. It
// checks arguments and options, calls package jqo and shapes the replies. It
// works on plain Go values rather than syscall/js so it can be tested without
// a browser; package main in ../../wasm only converts values at the boundary
// and recovers from panics.
package jsapi

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/wisebrian/jqo"
)

// Reply is what one call returns to JavaScript, as an object with the fields
// that are set: {error, result} on failure, {output, result} on success. A
// problem with the user's input also carries the *jqo.Error as JSON in
// Result, so the page can show the position and snippet.
type Reply struct {
	Err       string // the message to show; empty on success
	Result    string // JSON text for JSON.parse; empty when there is none
	Output    []byte // the produced bytes, for functions that produce some
	HasOutput bool   // distinguishes empty output from none
}

// Func is one function exposed to JavaScript. Each argument is nil (for
// undefined or null), []byte (from a Uint8Array or a string), float64, bool,
// or map[string]any (an options object whose values are of those types).
type Func func(args []any) Reply

// Funcs lists the functions by their JavaScript names.
var Funcs = map[string]Func{
	"parse":         parse,
	"format":        format,
	"repair":        repair,
	"tree":          tree,
	"detect":        detect,
	"decodeString":  decodeString,
	"decodeBase64":  decodeBase64,
	"decodePercent": decodePercent,
	"decodeJWT":     decodeJWT,
	"encodeString":  encodeString,
	"encodeBase64":  encodeBase64,
	"encodePercent": encodePercent,
	"encodeJWT":     encodeJWT,
}

// Now tells decodeJWT the time when the page doesn't pass one. The
// WebAssembly boundary sets it from Date.now(); package jqo itself never
// reads the clock.
var Now func() int64

// MaxInput is the most bytes any function accepts, so the boundary can refuse
// a larger argument before copying it into WebAssembly memory.
const MaxInput = jqo.DefaultMaxSize

// TooLarge is the reply for an argument refused at the boundary.
func TooLarge() Reply {
	return failure(&jqo.Error{Code: jqo.CodeTooLarge, Message: "the input is larger than the " + strconv.Itoa(MaxInput>>20) + " MB limit"})
}

// failure turns an error into a reply, attaching the structured form of a
// *jqo.Error.
func failure(err error) Reply {
	r := Reply{Err: err.Error()}
	if e, ok := err.(*jqo.Error); ok {
		b, _ := e.MarshalJSON()
		r.Result = string(b)
	}
	return r
}

func result(b []byte) Reply { return Reply{Result: string(b)} }

func output(b []byte, res []byte) Reply {
	if b == nil {
		b = []byte{}
	}
	return Reply{Output: b, HasOutput: true, Result: string(res)}
}

// argError is a mistake in how the page called a function, as opposed to a
// problem in the user's data, so it carries no structured result.
type argError string

// Error returns the message, which already names the function.
func (e argError) Error() string { return string(e) }

// args reads the arguments of one call. The first mistake sticks, so a
// function can read everything and check once.
type args struct {
	fn  string
	in  []any
	err error
}

func (a *args) fail(msg string) {
	if a.err == nil {
		a.err = argError(a.fn + ": " + msg)
	}
}

// bytes reads argument i, which must be a Uint8Array or a string.
func (a *args) bytes(i int, what string) []byte {
	if i >= len(a.in) || a.in[i] == nil {
		a.fail("the " + what + " is missing")
		return nil
	}
	b, ok := a.in[i].([]byte)
	if !ok {
		a.fail("the " + what + " must be a Uint8Array or a string")
	}
	return b
}

// optionalBytes is bytes for an argument that may be left out.
func (a *args) optionalBytes(i int, what string) []byte {
	if i >= len(a.in) || a.in[i] == nil {
		return nil
	}
	return a.bytes(i, what)
}

// number reads argument i as a number, or reports that it is absent.
func (a *args) number(i int, what string) (float64, bool) {
	if i >= len(a.in) || a.in[i] == nil {
		return 0, false
	}
	f, ok := a.in[i].(float64)
	if !ok {
		a.fail("the " + what + " must be a number")
	}
	return f, ok
}

// options reads an options object, rejecting unknown names: a misspelt
// option silently ignored is a bug that is hard to see from the page.
func (a *args) options(i int, allowed ...string) map[string]any {
	if i >= len(a.in) || a.in[i] == nil {
		return nil
	}
	m, ok := a.in[i].(map[string]any)
	if !ok {
		a.fail("the options must be an object")
		return nil
	}
	for k := range m {
		if !slices.Contains(allowed, k) {
			if len(allowed) == 0 {
				a.fail("takes no options, but got \"" + k + "\"")
			} else {
				a.fail("unknown option \"" + k + "\"; the options are " + strings.Join(allowed, ", "))
			}
		}
	}
	return m
}

func (a *args) flag(o map[string]any, k string) bool {
	v, ok := o[k]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		a.fail("option " + k + " must be true or false")
	}
	return b
}

func (a *args) text(o map[string]any, k string, choices ...string) string {
	v, ok := o[k]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok || !slices.Contains(choices, s) {
		a.fail("option " + k + " must be one of " + strings.Join(choices, ", "))
	}
	return s
}

func (a *args) count(o map[string]any, k string) int {
	v, ok := o[k]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) || f < 0 {
		a.fail("option " + k + " must be a whole number of at least 0")
		return 0
	}
	return int(f)
}

func parse(in []any) Reply {
	a := args{fn: "parse", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "lenient")
	lenient := a.flag(o, "lenient")
	if a.err != nil {
		return failure(a.err)
	}
	rep, err := jqo.Parse(data, jqo.Options{Lenient: lenient})
	if err != nil {
		return failure(err)
	}
	b, _ := rep.MarshalJSON()
	return result(b)
}

func format(in []any) Reply {
	a := args{fn: "format", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "lenient", "style", "sortKeys", "escapeNonASCII", "escapeHTML")
	opts := jqo.FormatOptions{
		Options:  jqo.Options{Lenient: a.flag(o, "lenient")},
		SortKeys: a.flag(o, "sortKeys"),
		Escape:   jqo.Escape{NonASCII: a.flag(o, "escapeNonASCII"), HTML: a.flag(o, "escapeHTML")},
	}
	switch a.text(o, "style", "indent2", "indent4", "tab", "minify") {
	case "indent4":
		opts.Style = jqo.Indent4
	case "tab":
		opts.Style = jqo.IndentTab
	case "minify":
		opts.Style = jqo.Minify
	}
	if a.err != nil {
		return failure(a.err)
	}
	res, err := jqo.Format(data, opts)
	if err != nil {
		return failure(err)
	}
	b, _ := res.MarshalJSON()
	return output(res.Output, b)
}

func detect(in []any) Reply {
	a := args{fn: "detect", in: in}
	data := a.bytes(0, "input")
	a.options(1)
	if a.err != nil {
		return failure(a.err)
	}
	return result(jqo.MarshalGuesses(jqo.Detect(data)))
}

func decodeString(in []any) Reply {
	a := args{fn: "decodeString", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "maxLayers")
	maxLayers := a.count(o, "maxLayers")
	if a.err != nil {
		return failure(a.err)
	}
	out, layers, err := jqo.DecodeString(data, maxLayers)
	if err != nil {
		return failure(err)
	}
	return output(out, []byte(`{"layers":`+strconv.Itoa(layers)+`}`))
}

func decodeBase64(in []any) Reply {
	a := args{fn: "decodeBase64", in: in}
	data := a.bytes(0, "input")
	a.options(1)
	if a.err != nil {
		return failure(a.err)
	}
	out, err := jqo.DecodeBase64(data)
	if err != nil {
		return failure(err)
	}
	return output(out, nil)
}

func decodePercent(in []any) Reply {
	a := args{fn: "decodePercent", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "keepPlus")
	keepPlus := a.flag(o, "keepPlus")
	if a.err != nil {
		return failure(a.err)
	}
	out, err := jqo.DecodePercent(data, keepPlus)
	if err != nil {
		return failure(err)
	}
	return output(out, nil)
}

func decodeJWT(in []any) Reply {
	a := args{fn: "decodeJWT", in: in}
	data := a.bytes(0, "token")
	now, ok := a.number(1, "time (seconds since 1970)")
	if a.err != nil {
		return failure(a.err)
	}
	t := int64(math.Floor(now))
	if !ok {
		if Now == nil {
			return failure(argError("decodeJWT: the time (seconds since 1970) is missing"))
		}
		t = Now()
	}
	j, err := jqo.DecodeJWT(data, t)
	if err != nil {
		return failure(err)
	}
	b, _ := j.MarshalJSON()
	return result(b)
}

func encodeString(in []any) Reply {
	a := args{fn: "encodeString", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "escapeNonASCII", "escapeHTML")
	esc := jqo.Escape{NonASCII: a.flag(o, "escapeNonASCII"), HTML: a.flag(o, "escapeHTML")}
	if a.err != nil {
		return failure(a.err)
	}
	out, err := jqo.EncodeString(data, esc)
	if err != nil {
		return failure(err)
	}
	return output(out, nil)
}

func encodeBase64(in []any) Reply {
	a := args{fn: "encodeBase64", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "url", "noPadding")
	opts := jqo.Base64Options{URL: a.flag(o, "url"), NoPadding: a.flag(o, "noPadding")}
	if a.err != nil {
		return failure(a.err)
	}
	out, err := jqo.EncodeBase64(data, opts)
	if err != nil {
		return failure(err)
	}
	return output(out, nil)
}

func encodePercent(in []any) Reply {
	a := args{fn: "encodePercent", in: in}
	data := a.bytes(0, "input")
	a.options(1)
	if a.err != nil {
		return failure(a.err)
	}
	out, err := jqo.EncodePercent(data)
	if err != nil {
		return failure(err)
	}
	return output(out, nil)
}

func encodeJWT(in []any) Reply {
	a := args{fn: "encodeJWT", in: in}
	header := a.bytes(0, "header")
	payload := a.bytes(1, "payload")
	secret := a.optionalBytes(2, "secret")
	if a.err != nil {
		return failure(a.err)
	}
	out, err := jqo.EncodeJWT(header, payload, secret)
	if err != nil {
		return failure(err)
	}
	return output(out, nil)
}

func repair(in []any) Reply {
	a := args{fn: "repair", in: in}
	data := a.bytes(0, "input")
	a.options(1)
	if a.err != nil {
		return failure(a.err)
	}
	res, err := jqo.Repair(data, jqo.Options{})
	if err != nil {
		return failure(err)
	}
	b, _ := res.MarshalJSON()
	return output(res.Output, b)
}

func tree(in []any) Reply {
	a := args{fn: "tree", in: in}
	data := a.bytes(0, "input")
	o := a.options(1, "lenient")
	lenient := a.flag(o, "lenient")
	if a.err != nil {
		return failure(a.err)
	}
	t, err := jqo.ParseTree(data, jqo.Options{Lenient: lenient})
	if err != nil {
		return failure(err)
	}
	b, _ := t.MarshalJSON()
	return result(b)
}
