package lossaudit_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/yberkut/cleanpass/"

// The audit proves the Merge Engine wrong or right, so it must share no code with it, not
// even through another package. Of this module it may know only the neutral model. Its tests
// may run the engine: go list does not follow the imports of tests.
func TestTheAuditUsesNoCodeOfTheMergeEngine(t *testing.T) {
	command := exec.Command("go", "list", "-deps", modulePath+"internal/lossaudit")
	command.Env = os.Environ()
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := strings.Fields(string(out))
	seen := 0
	for _, dep := range deps {
		if !strings.HasPrefix(dep, modulePath) {
			continue
		}
		seen++
		if dep != modulePath+"internal/lossaudit" && dep != modulePath+"internal/snapshot" {
			t.Errorf("the audit depends on %s", dep)
		}
	}
	if seen != 2 {
		t.Errorf("go list gave %d packages of this module, want the audit and the model: %v", seen, deps)
	}
}
