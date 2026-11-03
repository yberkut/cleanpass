//go:build !windows

package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// A signal from another process is as good as Ctrl+C here. Stdin is a pipe, so the echo of a terminal
// takes no part in this.
func TestAnInterruptWhileWritingRemovesTheTemporaryFile(t *testing.T) {
	for _, point := range []string{
		safewriter.PointPartialWrite,
		safewriter.PointBeforeSync,
		safewriter.PointReadBack,
		safewriter.PointAudit,
		safewriter.PointJudge,
		safewriter.PointPublish,
	} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			a, b := divergedPair(t, dir)
			output := filepath.Join(dir, "new.kdbx")
			t.Setenv(safewriter.FaultVariable, safewriter.FaultPausePrefix+point)

			process := testkit.Start(t, "merge", a.Path, b.Path, "--output", output)
			process.Send(t, testkit.Password)
			process.Send(t, testkit.Password)
			process.Send(t, "")
			process.WaitForStderr(t, safewriter.PausedAt+point)
			if found := tempFiles(t, dir); len(found) != 1 {
				t.Fatalf("temporary files at the pause: %q, want one", found)
			}
			process.Interrupt(t)

			got := process.Wait(t)
			wantCode(t, got, 130)
			wantNoTempFiles(t, dir)
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Errorf("a file lies at the target: %v", err)
			}
		})
	}
}
