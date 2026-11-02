//go:build !windows

package session

import "os"

// echoesTyping says stdin is a terminal the program cannot hide typing on. Everywhere but on
// Windows a terminal is a terminal to the program.
func echoesTyping(*os.File) bool { return false }
