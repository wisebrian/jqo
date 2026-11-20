package jqo

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCoreImports keeps the core free of anything that touches files, the
// network or the clock, and of the packages that would bloat the WebAssembly
// binary, including what they would bring in indirectly.
func TestCoreImports(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	forbidden := []string{"os", "time", "net", "syscall", "syscall/js", "fmt", "reflect", "encoding/json", "io/fs", "math/big", "math/rand"}
	for _, env := range [][]string{nil, {"GOOS=js", "GOARCH=wasm"}} {
		cmd := exec.Command(gobin, "list", "-deps", ".")
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list %v: %v", env, err)
		}
		for _, pkg := range strings.Fields(string(out)) {
			for _, f := range forbidden {
				if pkg == f || strings.HasPrefix(pkg, "crypto/") || strings.HasPrefix(pkg, "net/") {
					t.Errorf("%v: the core depends on %s", env, pkg)
					break
				}
			}
		}
	}
}
