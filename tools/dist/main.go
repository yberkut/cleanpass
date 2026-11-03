// Command dist builds cleanpass for every release target from one machine. Local builds
// and the release workflow both run it, so they cannot disagree about targets or flags.
// A Go program gives one command for every OS and shell.
//
//	go run ./tools/dist
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	mainPackage     = "github.com/yberkut/cleanpass/cmd/cleanpass"
	manualCheckFile = "cleanpass-manual-check.sh"
)

var targets = []struct{ goos, goarch string }{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "arm64"},
	{"darwin", "amd64"},
	{"windows", "amd64"},
}

func main() {
	dir := flag.String("o", "dist", "output directory")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []string{manualCheckFile}
	for _, target := range targets {
		name := "cleanpass-" + target.goos + "-" + target.goarch
		if target.goos == "windows" {
			name += ".exe"
		}
		// -trimpath keeps the paths of this machine out of a published file.
		build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(dir, name), mainPackage)
		// exec keeps the last value of a repeated variable, so these win over the shell.
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.goos, "GOARCH="+target.goarch)
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("%s/%s: %w", target.goos, target.goarch, err)
		}
		files = append(files, name)
	}
	if err := copyManualCheck(dir); err != nil {
		return err
	}
	slices.Sort(files)
	var sums strings.Builder
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644)
}

// copyManualCheck puts the script of the manual check beside the files. A person gets it the same
// way as the tool, and it finds the tool beside itself. It is found through the module, since this
// program can run from any directory of the repository.
func copyManualCheck(dir string) error {
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return fmt.Errorf("find the repository: %w", err)
	}
	script, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "scripts", "manual-check.sh"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manualCheckFile), script, 0o755)
}
