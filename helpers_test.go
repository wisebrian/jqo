package jqo

import (
	"errors"
	"testing"
)

// Tests let panics through, so a bug fails with its stack trace instead of
// being reported as CodeInternal.
func init() { rethrow = true }

// asError unwraps err as an *Error or fails the test.
func asError(t testing.TB, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v (%T) is not an *Error", err, err)
	}
	return e
}

// checkError verifies what every *Error promises: a code, a message, and a
// position and snippet that agree with each other and with the input.
func checkError(t testing.TB, data []byte, err error) *Error {
	t.Helper()
	e := asError(t, err)
	if e.Code == "" || e.Message == "" {
		t.Fatalf("error without code or message: %#v", e)
	}
	if e.Code == CodeInternal {
		t.Fatalf("internal error: %v", e)
	}
	if e.Pos.Line == 0 {
		if e.Pos != (Position{}) || e.Snippet != "" || e.Caret != "" {
			t.Fatalf("position-less error has position data: %#v", e)
		}
		return e
	}
	if e.Pos.Offset < 0 || e.Pos.Offset > len(data) || e.Pos.Column < 1 || e.Pos.UTF16 < e.Pos.Column-1 {
		t.Fatalf("impossible position %+v for %d bytes of input", e.Pos, len(data))
	}
	if len(e.Caret) == 0 || e.Caret[len(e.Caret)-1] != '^' {
		t.Fatalf("caret %q does not end in ^", e.Caret)
	}
	return e
}
