package main_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const (
	module       = "github.com/yberkut/cleanpass"
	library      = "github.com/tobischo/gokeepasslib/v3"
	gatewayPkg   = module + "/internal/gateway"
	testkitPkg   = module + "/internal/testkit"
	snapshotPkg  = module + "/internal/snapshot"
	mainPkg      = module + "/cmd/cleanpass"
	libraryCopy  = "third_party/gokeepasslib"
	listTemplate = `{{.ImportPath}}|{{join .Imports " "}}|{{join .TestImports " "}}|{{join .XTestImports " "}}`
)

// repoRoot is the directory of go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(goTool(t, "list", "-m", "-f", "{{.Dir}}"))
}

func goTool(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func lines(text string) []string {
	return strings.Split(strings.TrimSpace(text), "\n")
}

func TestBuildWithoutCgoGivesOneFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, testkit.BinaryName())
	build := exec.Command("go", "build", "-o", path, mainPkg)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the build gave %d files, want 1", len(entries))
	}
	if out, err := exec.Command(path, "--help").CombinedOutput(); err != nil {
		t.Errorf("the built file does not run: %v\n%s", err, out)
	}
}

func TestDirectDependenciesAreOnlyTheOnesTheSpecNames(t *testing.T) {
	t.Parallel()
	allowed := []string{library, "golang.org/x/term", "pgregory.net/rapid"}
	out := goTool(t, "list", "-m", "-f", "{{if not (or .Main .Indirect)}}{{.Path}}{{end}}", "all")
	for _, dependency := range lines(out) {
		if dependency != "" && !slices.Contains(allowed, dependency) {
			t.Errorf("direct dependency %s is not in the spec", dependency)
		}
	}
}

func TestLibraryIsAPatchedCopyInTheRepository(t *testing.T) {
	t.Parallel()
	replaced := strings.TrimSpace(goTool(t, "list", "-m", "-f", "{{.Replace.Path}}", library))
	if filepath.ToSlash(replaced) != "./"+libraryCopy {
		t.Errorf("the library comes from %q, want ./%s", replaced, libraryCopy)
	}
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, libraryCopy, "PATCHES.md")); err != nil {
		t.Errorf("no note about the patch beside the copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, libraryCopy, "tests", "kdbx4", "example.kdbx")); err != nil {
		t.Errorf("the copy has no tests folder: %v", err)
	}
}

// A change of the copy must pass the tests of the library itself. Those are in another
// module, so `go test ./...` from the root would not see them otherwise.
func TestLibraryCopyPassesItsOwnTests(t *testing.T) {
	t.Parallel()
	test := exec.Command("go", "test", "./...")
	test.Dir = filepath.Join(repoRoot(t), libraryCopy)
	if out, err := test.CombinedOutput(); err != nil {
		t.Errorf("tests of the library copy: %v\n%s", err, out)
	}
}

func TestOnlyTheGatewayKnowsTheLibrary(t *testing.T) {
	t.Parallel()
	for _, line := range lines(goTool(t, "list", "-f", listTemplate, module+"/...")) {
		parts := strings.Split(line, "|")
		pkg, imports := parts[0], strings.Fields(strings.Join(parts[1:], " "))
		if pkg == gatewayPkg || pkg == testkitPkg {
			continue
		}
		for _, imported := range imports {
			if imported == library || strings.HasPrefix(imported, library+"/") {
				t.Errorf("package %s imports %s", pkg, imported)
			}
		}
	}
}

func TestToolDoesNotDependOnTheTestKit(t *testing.T) {
	t.Parallel()
	if deps := lines(goTool(t, "list", "-deps", mainPkg)); slices.Contains(deps, testkitPkg) {
		t.Errorf("the tool imports %s", testkitPkg)
	}
}

func TestSnapshotDependsOnlyOnTheStandardLibrary(t *testing.T) {
	t.Parallel()
	out := goTool(t, "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", snapshotPkg)
	for _, dep := range lines(out) {
		if dep != "" && dep != snapshotPkg {
			t.Errorf("Snapshot depends on %s", dep)
		}
	}
}

var (
	cyrillicLetter = regexp.MustCompile(`\p{Cyrillic}`)
	wordInFormat   = regexp.MustCompile(`[A-Za-z]{2,}`)
	formatVerb     = regexp.MustCompile(`%[-+# 0]*\d*(\.\d+)?[a-zA-Z]`)
)

// Text for the human lives in internal/text. The test package cannot see intent, so it
// checks the two things a slip would leave behind: Cyrillic letters in any other
// package, and a sentence in a call that writes to a stream.
func TestTextForTheHumanLivesInOnePlace(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	fset := token.NewFileSet()
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "text", "testkit", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.BasicLit:
					if value, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING && cyrillicLetter.MatchString(value) {
						t.Errorf("%s: Ukrainian text outside internal/text: %s", fset.Position(n.Pos()), n.Value)
					}
				case *ast.CallExpr:
					checkOutputCall(t, fset, n)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func checkOutputCall(t *testing.T, fset *token.FileSet, call *ast.CallExpr) {
	t.Helper()
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" || !strings.Contains(selector.Sel.Name, "rint") || strings.HasPrefix(selector.Sel.Name, "S") {
		return
	}
	for _, arg := range call.Args {
		literal, ok := arg.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			continue
		}
		if wordInFormat.MatchString(formatVerb.ReplaceAllString(value, "")) {
			t.Errorf("%s: text for the human outside internal/text: %s", fset.Position(call.Pos()), literal.Value)
		}
	}
}
