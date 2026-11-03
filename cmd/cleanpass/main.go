// Command cleanpass works safely with diverged copies of a KeePassXC database.
package main

import (
	"os"

	"github.com/yberkut/cleanpass/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}
