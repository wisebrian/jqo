// Package jqo validates, formats, decodes and encodes JSON with only the
// standard library, so it can run in a browser as WebAssembly and the data it
// handles never leaves the visitor's machine.
//
// Parse checks a document against RFC 8259. For valid input it reports
// warnings and a summary. The warnings cover duplicate keys, integers that
// JavaScript can't hold exactly and lone surrogate escapes. For invalid input
// the *Error points at the first problem by byte offset, line and column, in
// words a non-programmer can act on. Options.Lenient also accepts comments,
// trailing commas and a byte order mark, and Repair removes them.
//
// Format lays a document out again without re-encoding its values, so key
// order, number literals and string escapes survive byte for byte. ParseTree
// lists the nodes for a collapsible view.
//
// The Decode and Encode functions move JSON in and out of the wrappers it is
// often found in: JSON string literals (including JSON encoded twice), base64,
// URL percent-encoding and JSON Web Tokens. Detect suggests which decoder an
// input needs; it never decodes on a guess.
//
// The package reads no files, opens no connections and never looks at the
// clock, so a result depends only on the arguments. Every exported function
// returns an error rather than panicking, whatever bytes it is given.
package jqo
