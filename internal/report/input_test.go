package report_test

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	reportPkg   = "github.com/yberkut/cleanpass/internal/report"
	mergePkg    = "github.com/yberkut/cleanpass/internal/merge"
	snapshotPkg = "github.com/yberkut/cleanpass/internal/snapshot"
)

// valueCarriers are the types that hold the secret values of a copy. Anything that reaches one of
// them can reach a secret: a Snapshot, an Entry or a merge.Result reach Field.
var valueCarriers = []string{
	snapshotPkg + ".Field",
	snapshotPkg + ".Attachment",
	snapshotPkg + ".CustomDataItem",
	snapshotPkg + ".AutoType",
	"github.com/yberkut/cleanpass/internal/gateway.Credentials",
}

// valueName is a name a text field must not have in the input of the Report.
var valueName = regexp.MustCompile(`(?i)password|secret|value|content|credential|keyfile`)

// inputCheck walks a type down to its smallest parts and says what could carry a secret value.
type inputCheck struct {
	seen     map[types.Type]bool
	problems []string
}

func (c *inputCheck) walk(where string, ty types.Type) {
	ty = types.Unalias(ty)
	if c.seen[ty] {
		return
	}
	c.seen[ty] = true
	switch t := ty.(type) {
	case *types.Basic:
		if t.Kind() == types.UnsafePointer {
			c.problem(where, "an unsafe pointer can point to anything")
		}
	case *types.Named:
		name := t.String()
		switch {
		case name == "time.Time":
		case slices.Contains(valueCarriers, name):
			c.problem(where, name+" holds secret values")
		default:
			c.walk(where+" "+t.Obj().Name(), t.Underlying())
		}
	case *types.Pointer:
		c.walk(where, t.Elem())
	case *types.Array:
		c.walk(where, t.Elem())
	case *types.Map:
		c.walk(where, t.Key())
		c.walk(where, t.Elem())
	case *types.Slice:
		if basic, ok := types.Unalias(t.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Byte {
			c.problem(where, "a slice of bytes can hold any content")
		}
		c.walk(where, t.Elem())
	case *types.Struct:
		for field := range t.Fields() {
			fieldWhere := where + "." + field.Name()
			if basic, ok := field.Type().Underlying().(*types.Basic); ok && basic.Info()&types.IsString != 0 && valueName.MatchString(field.Name()) {
				c.problem(fieldWhere, "its name says it holds a value")
			}
			c.walk(fieldWhere, field.Type())
		}
	case *types.Interface:
		c.problem(where, "an interface can hold anything: "+t.String())
	default:
		c.problem(where, fmt.Sprintf("%T can hide anything", t))
	}
}

func (c *inputCheck) problem(where, why string) {
	c.problems = append(c.problems, where+": "+why)
}

// parameters walks the parameters of a function. The stream it writes to is where the output goes,
// and no input.
func (c *inputCheck) parameters(where string, signature *types.Signature) {
	for parameter := range signature.Params().Variables() {
		if parameter.Type().String() != "io.Writer" {
			c.walk(where, parameter.Type())
		}
	}
}

func newInputCheck() *inputCheck { return &inputCheck{seen: map[types.Type]bool{}} }

// loadPackage reads a package with its types as the compiler sees them.
func loadPackage(t *testing.T, path string) *types.Package {
	t.Helper()
	out, err := exec.Command("go", "list", "-export", "-deps", "-f", "{{.ImportPath}}={{.Export}}", path).CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	exports := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		name, file, _ := strings.Cut(strings.TrimSpace(line), "=")
		exports[name] = file
	}
	imports := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(exports[path])
	})
	pkg, err := imports.Import(path)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// The Report must not be able to receive a secret value, whatever its code does. So this reads
// every function, method and type the package offers, and everything they reach.
func TestTheInputOfTheReportCannotHoldASecretByItsType(t *testing.T) {
	report := loadPackage(t, reportPkg)
	check := newInputCheck()
	functions := 0
	scope := report.Scope()
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		if !object.Exported() {
			continue
		}
		switch o := object.(type) {
		case *types.Func:
			functions++
			check.parameters(name, o.Signature())
		case *types.TypeName:
			check.walk(name, o.Type())
			if named, ok := o.Type().(*types.Named); ok {
				for method := range named.Methods() {
					if method.Exported() {
						functions++
						check.parameters(name+"."+method.Name(), method.Signature())
					}
				}
			}
		}
	}
	if functions < 3 {
		t.Errorf("only %d functions were read: the check looks at the wrong package", functions)
	}
	for _, problem := range check.problems {
		t.Errorf("the input of the Report can carry a secret: %s", problem)
	}

	// The check must have read what the Report takes from a merge, to the end.
	reached := map[string]bool{}
	for ty := range check.seen {
		reached[ty.String()] = true
	}
	for _, want := range []string{mergePkg + ".Facts", mergePkg + ".EntryChange", mergePkg + ".SimilarPair", mergePkg + ".DeletionChange", mergePkg + ".Label", mergePkg + ".Difference"} {
		if !reached[want] {
			t.Errorf("the check did not reach %s", want)
		}
	}
}

// A check that finds nothing proves something only if it does find what it looks for.
func TestTheCheckOfTheInputCatchesWhatCarriesASecret(t *testing.T) {
	merge, snapshot := loadPackage(t, mergePkg), loadPackage(t, snapshotPkg)
	for _, tc := range []struct {
		pkg  *types.Package
		name string
	}{
		{snapshot, "Snapshot"},
		{snapshot, "Group"},
		{snapshot, "Entry"},
		{snapshot, "EntryVersion"},
		{snapshot, "Field"},
		{snapshot, "Attachment"},
		{snapshot, "Meta"},
		{merge, "Result"},
		{merge, "Input"},
		{merge, "Copy"},
		{merge, "Question"},
		{merge, "Event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := tc.pkg.Scope().Lookup(tc.name)
			if object == nil {
				t.Fatalf("no type %s in %s", tc.name, tc.pkg.Path())
			}
			check := newInputCheck()
			check.walk(tc.name, object.Type())
			if len(check.problems) == 0 {
				t.Errorf("the check takes %s for safe", tc.name)
			}
		})
	}
}
