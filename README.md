# jqo

A JSON toolkit in Go, standard library only, built to run in the browser as
WebAssembly so the data it handles never leaves the visitor's machine. It
does four things:

- **Parse**: validates JSON against RFC 8259 and points at the first problem by
  line, column and byte offset, in words a non-programmer can act on. A missing
  comma or bracket is pointed at where it belongs, judging by the indentation,
  rather than where parsing stopped, which can be lines further down. It also
  warns about valid JSON that is probably a mistake (duplicate keys, integers
  JavaScript can't hold exactly, lone surrogate escapes), summarises the
  document and lists its nodes for a tree view. Lenient mode also accepts
  comments, trailing commas and a byte order mark, and can remove them.
- **Format**: pretty-prints with 2 spaces, 4 spaces or a tab, or minifies. It can
  sort keys and escape non-ASCII or HTML characters. Number and string
  literals are copied byte for byte, so `1.0` stays `1.0` and
  `12345678901234567890` stays exact.
- **Decode**: unwraps JSON found inside other things, such as a JSON string
  literal (even if it was encoded twice or three times), base64 or base64url,
  URL percent-encoding, or a JSON Web Token. It can also guess which of these
  an input is.
- **Encode**: does the reverse. It turns text into a JSON string literal, and
  produces base64, percent-encoding, or a JWT that is unsigned or signed with
  HS256, HS384 or HS512.

The core never reads files, opens connections or looks at the clock, and no
input can make it panic: every function returns an error instead.

## From Go

```go
import "github.com/wisebrian/jqo"

rep, err := jqo.Parse(data, jqo.Options{})
if err != nil {
	fmt.Println(err) // Line 3, column 15: expected , or } after the value
	var e *jqo.Error
	if errors.As(err, &e) {
		fmt.Println(e.Code)    // expected-comma-or-close
		fmt.Println(e.Snippet) // the offending line…
		fmt.Println(e.Caret)   // …and a ^ under the spot
	}
}

res, err := jqo.Format(data, jqo.FormatOptions{Style: jqo.Indent2, SortKeys: true})
tok, err := jqo.DecodeJWT(token, time.Now().Unix())
```

`go doc github.com/wisebrian/jqo` lists the whole API.

## In the browser

Build the WebAssembly binary and copy the matching JavaScript glue from the
same Go installation:

```sh
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o jqo.wasm ./wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .
```

With Go 1.27.4 the binary is 2.7 MB, or 757 KB after `gzip -9`.

The functions are registered on a global object, `jqo`. Every function is
synchronous and is meant to run in a Web Worker:

```js
importScripts("wasm_exec.js");
const go = new Go();
const ready = WebAssembly.instantiateStreaming(fetch("jqo.wasm"), go.importObject)
  .then(({ instance }) => { go.run(instance); }); // registers jqo.* right away

onmessage = async ({ data }) => {
  await ready;
  const r = jqo.format(new TextEncoder().encode(data.text), { style: "indent2", sortKeys: true });
  if (r.error) {
    // r.result, when present, is the structured error: code, pos, snippet, caret.
    postMessage({ error: r.error, detail: r.result && JSON.parse(r.result) });
    return;
  }
  postMessage({ text: new TextDecoder().decode(r.output), fixes: JSON.parse(r.result).fixes });
};
```

Arguments are `Uint8Array`s (strings work too); options are plain objects,
and an unknown option is an error, so typos don't go unnoticed. A call
returns an object with some of these fields:

| Field    | Meaning                                                                  |
|----------|--------------------------------------------------------------------------|
| `output` | a `Uint8Array` with the produced bytes                                   |
| `result` | a JSON string for `JSON.parse`, with the structured result               |
| `error`  | a message ready to show, such as `Line 3, column 15: expected , or }…`   |

On failure, `error` is set. If the problem is in the user's data, `result`
also holds the error as JSON: `code`, `message`, `text`, `pos` (with
`offset`, `line`, `column` and `utf16`), `snippet` and `caret`. The `utf16`
offset is what a textarea's `setSelectionRange` needs. A panic inside Go is
caught and comes back as `{error: "internal error in …"}`; it is never thrown.

| Function | Options | Returns |
|---|---|---|
| `parse(input, opts)` | `lenient` | `result`: `{summary, warnings, warningCount, fixes, fixCount}` |
| `format(input, opts)` | `style` (`"indent2"`, `"indent4"`, `"tab"`, `"minify"`), `sortKeys`, `escapeNonASCII`, `escapeHTML`, `lenient` | `output`, `result`: `{fixes, fixCount}` |
| `repair(input)` | | `output` (strict JSON), `result`: `{fixes, fixCount}` |
| `tree(input, opts)` | `lenient` | `result`: `{nodes: [{kind, pointer, key, index, depth, parent, value, len, start, end, utf16Start, utf16End}]}` |
| `detect(input)` | | `result`: `[{encoding, confidence, reason}]`, most likely first |
| `decodeString(input, opts)` | `maxLayers` | `output`, `result`: `{layers}` |
| `decodeBase64(input)` | | `output` |
| `decodePercent(input, opts)` | `keepPlus` | `output` |
| `decodeJWT(token, nowSeconds)` | | `result`: header, payload, `alg`, `times` (exp, iat, nbf as UTC dates), `expired`, `notYetValid`, `signatureChecked: false` |
| `encodeString(input, opts)` | `escapeNonASCII`, `escapeHTML` | `output` |
| `encodeBase64(input, opts)` | `url`, `noPadding` | `output` |
| `encodePercent(input)` | | `output` |
| `encodeJWT(header, payload, secret)` | | `output` |

`decodeJWT` uses `Date.now()` when `nowSeconds` is left out. It never checks
signatures: decoding a token says nothing about whether it is genuine.

## Limits

| Limit | Default | Why |
|---|---|---|
| Input | 4 MB (2 MB for `tree`) | keeps the page responsive and WebAssembly memory small |
| Format output | 32 MB | indentation grows with depth, so 20 KB of nested brackets would pretty-print to 200 MB |
| Nesting depth | 10 000 | the same as `encoding/json`, so both agree on what is valid |
| Tree nodes | 200 000 | a larger tree view is unusable and costly to hold |
| Warnings and fixes listed | 1 000 | the counts go on past it |

Going over a limit is an error with code `too-large` or `too-deep`.

In Go, `Options` and `FormatOptions` can change the limits.

## Differences from encoding/json

Strict mode agrees with `json.Valid` except on invalid UTF-8 inside strings.
`json.Valid` accepts it, but RFC 8259 §8.1 requires UTF-8, so jqo rejects it
and says where it is.

The tests check this claim against a second implementation, `jsontext`. On
lone surrogate escapes the two references disagree with each other: `jsontext`
rejects them, while jqo and `json.Valid` accept them with a warning.

Error positions differ on purpose. Every parser stops at the first byte that
can't continue the document, and `encoding/json`, `jsontext` and jqo all stop
at the same byte, but for a missing comma that byte is the next key, a line
down, and for a missing `}` it can be the end of the input. When parsing has
failed, jqo reads the indentation the way a person would and moves the error
to where the comma or bracket belongs; the message says where parsing stopped:

```text
Line 4, column 16: the object that starts at line 2, column 12 is not closed:
a } is missing here; the } at line 6, column 1 closes it instead
```

Minified JSON, contents not indented under their bracket, and tabs mixed with
spaces give it nothing to go on, and the error stays where parsing stopped.

## Testing

```sh
go test ./...                          # unit, golden, differential and round-trip tests
go test -run TestFormatGolden -update  # rewrite testdata/format/*.golden after a deliberate change
go test -fuzz FuzzParse -fuzztime 60s  # one of 14 fuzz targets; see *_test.go
```

The tests compare jqo with independent implementations:

- `json.Valid`, `jsontext`, `json.Compact`, `json.Indent` and `json.HTMLEscape`
  for parsing and formatting;
- `crypto/hmac` and `crypto/sha256`/`sha512` for the in-house SHA-2, which
  keeps `os` and `time` out of the core;
- package `time` for the dates in JWTs.

## License

MIT
