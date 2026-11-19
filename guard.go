package jqo

// rethrow makes guard let panics through. Tests set it so that a bug fails
// with its stack trace instead of a polite CodeInternal error.
var rethrow = false

// guard is deferred by every exported function that can fail. It turns a
// panic into an *Error with CodeInternal, because a program that embeds this
// package must not crash on any input; in WebAssembly a panic would end every
// later call too.
func guard[T any](res *T, err *error) {
	if rethrow {
		return
	}
	if r := recover(); r != nil {
		var zero T
		*res = zero
		*err = internalError(r)
	}
}

// guard2 is guard for functions with two results besides the error.
func guard2[T, U any](a *T, b *U, err *error) {
	if rethrow {
		return
	}
	if r := recover(); r != nil {
		var za T
		var zb U
		*a, *b = za, zb
		*err = internalError(r)
	}
}

func internalError(r any) *Error {
	return &Error{Code: CodeInternal, Message: "internal error: " + panicText(r) + " (this is a bug in jqo)"}
}

func panicText(r any) string {
	switch v := r.(type) {
	case error:
		return v.Error()
	case string:
		return v
	}
	return "unexpected panic"
}
