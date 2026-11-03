package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
)

// mergeCommand gives the command that merges the copies, ready to copy. It repeats the
// arguments of this run as they came, so an option of a copy stays beside its file. After
// -- every word is a file, so --output and any other option of the command go in front of it.
func mergeCommand(args []string, firstPath string, options ...string) string {
	at := slices.Index(args, "--")
	if at < 0 {
		at = len(args)
	}
	words := slices.Concat(
		[]string{filepath.ToSlash(os.Args[0]), "merge"},
		args[:at],
		options,
		[]string{"--output", freePath(firstPath)},
		args[at:],
	)
	for i, word := range words {
		words[i] = shellWord(word)
	}
	return strings.Join(words, " ")
}

// freePath gives a path for the result beside the first copy that nothing occupies yet.
func freePath(firstPath string) string {
	ext := filepath.Ext(firstPath)
	stem := strings.TrimSuffix(firstPath, ext)
	path := stem + "-merged" + ext
	for n := 2; ; n++ {
		if _, err := os.Lstat(path); err != nil {
			return path
		}
		path = fmt.Sprintf("%s-merged-%d%s", stem, n, ext)
	}
}

// shellWord quotes a word when a shell would change it. On Windows double quotes work in
// cmd, PowerShell and Git Bash, and a file name cannot hold one.
func shellWord(word string) string {
	safe := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:=+,@", r)
	}
	if word != "" && !strings.ContainsFunc(word, func(r rune) bool { return !safe(r) }) {
		return word
	}
	if runtime.GOOS == "windows" {
		return `"` + word + `"`
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}
