package merge_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The engine is a pure function. These names let code reach files, the clock, the
// terminal, the network or chance. crypto/rand may appear only where the key is made.
var forbiddenImports = []string{
	"bufio", "crypto/rand", "log", "log/slog", "math/rand", "math/rand/v2", "net", "net/http",
	"io", "io/fs", "io/ioutil", "os", "os/exec", "os/signal", "path/filepath", "runtime", "syscall",
	"github.com/yberkut/cleanpass/internal/gateway",
	"github.com/yberkut/cleanpass/internal/report",
	"github.com/yberkut/cleanpass/internal/session",
	"github.com/yberkut/cleanpass/internal/cli",
	"github.com/yberkut/cleanpass/internal/text",
}

var forbiddenCalls = map[string][]string{
	"time": {"Now", "Since", "Until", "Sleep", "After", "AfterFunc", "Tick", "NewTimer", "NewTicker"},
	"fmt":  {"Print", "Printf", "Println", "Fprint", "Fprintf", "Fprintln", "Scan", "Scanf", "Scanln", "Fscan", "Fscanf", "Fscanln"},
}

func TestTheEngineTouchesNoFilesNoClockAndNoInputOrOutput(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	fset := token.NewFileSet()
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(forbiddenImports, imported) && !(imported == "crypto/rand" && path == "key.go") {
				t.Errorf("%s imports %s", path, imported)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && slices.Contains(forbiddenCalls[pkg.Name], selector.Sel.Name) {
					t.Errorf("%s uses %s.%s", fset.Position(selector.Pos()), pkg.Name, selector.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no source files of the engine")
	}
}

// NewKey may read the system, but a merge must not depend on it: the same Input gives the
// same Result, so Run itself makes no key.
func TestTheSameInputGivesTheSameResult(t *testing.T) {
	in := input(named(twoDivergedCopies(0)...)...)

	first, second := mustRun(t, in), mustRun(t, in)

	if printed(first.Questions) != printed(second.Questions) ||
		printed(first.Merged.Root) != printed(second.Merged.Root) ||
		printed(first.Report) != printed(second.Report) {
		t.Error("two runs on the same input differ")
	}
}

// The spec allows rapid only for tests. The generator imports it, so the tool must not
// import the generator.
func TestTheToolDependsOnNeitherRapidNorTheGenerator(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "github.com/yberkut/cleanpass/cmd/cleanpass")
	command.Env = os.Environ()
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "pgregory.net/rapid" || strings.HasSuffix(dep, "/merge/mergetest") {
			t.Errorf("the tool depends on %s", dep)
		}
	}
}
