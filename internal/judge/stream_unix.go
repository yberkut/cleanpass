//go:build !windows

package judge

import (
	"io"
	"os/exec"
)

// attachmentStream names where attachment-export can write so that the bytes reach out and no
// file: its own standard output, under the name of a file. Its --stdout option would not do. It
// sends bytes through a text stream, and that changes what is not valid UTF-8.
//
// With --quiet nothing else goes to that stream.
func attachmentStream(cmd *exec.Cmd, out io.Writer) (target string, finish func() error, err error) {
	cmd.Stdout = out
	return "/dev/stdout", func() error { return nil }, nil
}
