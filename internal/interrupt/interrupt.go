// Package interrupt ends the process in one place when the person presses Ctrl+C or the system
// asks it to stop. Whoever holds something that must not stay behind, such as a terminal without
// echo or a temporary file, registers a step here for the time it holds it.
package interrupt

import (
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
)

// ExitCode is the exit code after an interrupt, the one shells use for Ctrl+C.
const ExitCode = 130

var (
	// mu stays locked once an interrupt has begun, so whoever comes later waits for the end of
	// the process and cannot get ahead of the steps.
	mu    sync.Mutex
	steps []*func()
	once  sync.Once
)

func watch() {
	once.Do(func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			<-signals
			mu.Lock()
			for _, step := range slices.Backward(steps) {
				(*step)()
			}
			os.Exit(ExitCode)
		}()
	})
}

// Guard adds a step for an interrupt, and the steps run from the last one added to the first. The
// result takes the step away again. A step must not call this package.
func Guard(step func()) (release func()) {
	watch()
	entry := &step
	mu.Lock()
	defer mu.Unlock()
	steps = append(steps, entry)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		steps = slices.DeleteFunc(steps, func(other *func()) bool { return other == entry })
	}
}

// Finish waits for the steps of an interrupt that has begun. A command that ends at that moment
// must not end the process before they are done.
func Finish() {
	mu.Lock()
	defer mu.Unlock()
}
