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
	"strings"
)

const mainPackage = "github.com/yberkut/cleanpass/cmd/cleanpass"

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
	var sums strings.Builder
	for _, target := range targets {
		name := "cleanpass-" + target.goos + "-" + target.goarch
		if target.goos == "windows" {
			name += ".exe"
		}
		path := filepath.Join(dir, name)
		// -trimpath keeps the paths of this machine out of a published file.
		build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", path, mainPackage)
		// exec keeps the last value of a repeated variable, so these win over the shell.
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.goos, "GOARCH="+target.goarch)
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("%s/%s: %w", target.goos, target.goarch, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644)
}
