//go:build js && wasm

// Command wasm is the WebAssembly build of jqo. It registers the functions of
// package jsapi on a global object named jqo, for a page or a Web Worker to
// call synchronously:
//
//	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o jqo.wasm ./wasm
//
// Each function takes Uint8Array (or string) arguments and an optional
// options object, and returns {output, result} on success or {error, result}
// on failure, where output is a Uint8Array and result a JSON string.
package main

import (
	"syscall/js"

	"github.com/wisebrian/jqo/internal/jsapi"
)

var (
	uint8Array   = js.Global().Get("Uint8Array")
	stringObject = js.Global().Get("String")
)

func main() {
	jsapi.Now = func() int64 { return int64(js.Global().Get("Date").Call("now").Float()) / 1000 }
	api := js.Global().Get("Object").New()
	for name, fn := range jsapi.Funcs {
		api.Set(name, js.FuncOf(wrap(name, fn)))
	}
	js.Global().Set("jqo", api)
	select {} // keep the Go program alive to serve calls
}

// wrap adapts fn to JavaScript. It recovers from any panic, because a panic
// would end the Go program and make every later call fail.
func wrap(name string, fn jsapi.Func) func(js.Value, []js.Value) any {
	return func(_ js.Value, args []js.Value) (ret any) {
		defer func() {
			if r := recover(); r != nil {
				msg := "unexpected panic"
				switch v := r.(type) {
				case error:
					msg = v.Error()
				case string:
					msg = v
				}
				ret = map[string]any{"error": "internal error in " + name + ": " + msg}
			}
		}()
		in := make([]any, len(args))
		for i, a := range args {
			v, ok, msg := toGo(a)
			if !ok {
				if msg == "" {
					return toJS(jsapi.TooLarge())
				}
				return map[string]any{"error": name + ": " + msg}
			}
			in[i] = v
		}
		return toJS(fn(in))
	}
}

// toGo converts one argument. ok is false with an empty msg when the
// argument is too large to copy.
func toGo(v js.Value) (any, bool, string) {
	switch v.Type() {
	case js.TypeUndefined, js.TypeNull:
		return nil, true, ""
	case js.TypeBoolean:
		return v.Bool(), true, ""
	case js.TypeNumber:
		return v.Float(), true, ""
	case js.TypeString:
		// A string's UTF-8 form is never shorter than its UTF-16 length, so
		// a longer string is refused before it is copied. Length panics on
		// a string, which is not an object; its String wrapper has one.
		if stringObject.New(v).Length() > jsapi.MaxInput {
			return nil, false, ""
		}
		return []byte(v.String()), true, ""
	case js.TypeObject:
		if v.InstanceOf(uint8Array) {
			n := v.Length()
			if n > jsapi.MaxInput {
				return nil, false, ""
			}
			b := make([]byte, n)
			js.CopyBytesToGo(b, v)
			return b, true, ""
		}
		// Object.entries reads the options through Call, so a getter that
		// throws becomes a panic that wrap recovers. v.Get would let the
		// exception escape into the caller, past the recover.
		entries := js.Global().Get("Object").Call("entries", v)
		m := make(map[string]any, entries.Length())
		for i := 0; i < entries.Length(); i++ {
			e := entries.Index(i)
			k := e.Index(0).String()
			switch x := e.Index(1); x.Type() {
			case js.TypeBoolean:
				m[k] = x.Bool()
			case js.TypeNumber:
				m[k] = x.Float()
			case js.TypeString:
				m[k] = x.String()
			case js.TypeUndefined, js.TypeNull:
			default:
				return nil, false, "option " + k + " must be a boolean, a number or a string"
			}
		}
		return m, true, ""
	}
	return nil, false, "arguments must be Uint8Arrays, strings, numbers or option objects"
}

func toJS(r jsapi.Reply) any {
	obj := map[string]any{}
	if r.Err != "" {
		obj["error"] = r.Err
	}
	if r.HasOutput {
		u := uint8Array.New(len(r.Output))
		js.CopyBytesToJS(u, r.Output)
		obj["output"] = u
	}
	if r.Result != "" {
		obj["result"] = r.Result
	}
	return obj
}
