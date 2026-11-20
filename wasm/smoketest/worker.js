importScripts("wasm_exec.js");
const results = [];
function check(name, ok, detail) { results.push({name, ok: !!ok, detail}); }
const enc = (s) => new TextEncoder().encode(s);
const dec = (b) => new TextDecoder().decode(b);
// reply makes a call's result comparable: output as text, not bytes
const reply = (r) => ({error: r.error, result: r.result, output: r.output && dec(r.output)});

(async () => {
  try {
    const go = new Go();
    const {instance} = await WebAssembly.instantiateStreaming(fetch("jqo.wasm"), go.importObject);
    go.run(instance);
    const names = ["parse", "format", "repair", "tree", "detect", "decodeString", "decodeBase64",
      "decodePercent", "decodeJWT", "encodeString", "encodeBase64", "encodePercent", "encodeJWT"];
    check("global object", typeof self.jqo === "object" && names.every((n) => typeof jqo[n] === "function"), Object.keys(self.jqo || {}));

    // Every function takes strings as well as Uint8Arrays, with the same reply.
    const doc = '{"b": [1, 2.50, "é"], "a": null}';
    const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.8qZF8vbN3UpcanXFc-mPXJkOPN01-bRch8XX3rToP1U";
    const calls = [
      ["parse", [doc]],
      ["format", [doc, {style: "indent2", sortKeys: true}]],
      ["repair", ['{"a": 1, // note\n}']],
      ["tree", [doc]],
      ["detect", [jwt]],
      ["decodeString", ['"{\\"a\\":1}"']],
      ["decodeBase64", ["eyJhIjoxfQ"]],
      ["decodePercent", ["%7B%22a%22%3A1%7D"]],
      ["decodeJWT", [jwt, 1795000000]],
      ["encodeString", ['say "hi"']],
      ["encodeBase64", ["hello", {url: true, noPadding: true}]],
      ["encodePercent", ["a b&c"]],
      ["encodeJWT", ['{"alg":"HS256","typ":"JWT"}', '{"sub":"1"}', "secret"]],
    ];
    for (const [fn, args] of calls) {
      const fromStrings = reply(jqo[fn](...args));
      const fromBytes = reply(jqo[fn](...args.map((a) => typeof a === "string" ? enc(a) : a)));
      check(fn + " takes strings", !fromStrings.error && JSON.stringify(fromStrings) === JSON.stringify(fromBytes), {fromStrings, fromBytes});
    }
    check("encodeJWT signs", dec(jqo.encodeJWT('{"alg":"HS256","typ":"JWT"}', '{"sub":"1"}', "secret").output) === jwt);

    // positions count UTF-16 units, as a textarea's selection does
    let r = jqo.parse('{"é😀": True}');
    const e = r.result && JSON.parse(r.result);
    check("error position in UTF-16", e && e.code === "invalid-literal" && e.pos.utf16 === '{"é😀": '.length && e.pos.column === 8, r);

    // limits are enforced before an argument is copied into Go
    const big = "x".repeat((4 << 20) + 1);
    r = jqo.parse(big);
    check("long string refused", r.error && JSON.parse(r.result).code === "too-large", r.error);
    r = jqo.parse(enc(big));
    check("long Uint8Array refused", r.error && JSON.parse(r.result).code === "too-large", r.error);

    r = jqo.format(doc, {stlye: "indent2"});
    check("misspelt option", r.error && /stlye/.test(r.error) && !r.result, r);
    r = jqo.parse(42);
    check("wrong argument type", r.error && !r.result, r);
    try {
      r = jqo.format(doc, {get style() { throw new Error("boom from a getter"); }});
    } catch (err) {
      r = {thrown: String(err)};
    }
    check("panic recovered", /^internal error in format: .*boom/.test(r.error || ""), r);
    r = jqo.parse(doc);
    check("alive after panic", !r.error && JSON.parse(r.result).summary.keys === 2, r);
  } catch (e) {
    check("exception escaped", false, String(e && e.stack || e));
  }
  postMessage(results);
})();
