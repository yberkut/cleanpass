package judge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/safewriter"
)

// ErrNotFound means keepassxc-cli is neither on PATH nor in a standard install place. With this
// error and with an UnusableError the work goes on, and the person gets a warning.
var ErrNotFound = errors.New("keepassxc-cli not found")

// UnusableError means the program is there, but it cannot judge a result.
type UnusableError struct {
	Path string
	// Version is what the program says about itself when it is too old. An empty one means
	// that the program does not run.
	Version string
}

func (e *UnusableError) Error() string { return e.Path + " cannot judge a result" }

// FaultNoInstallPlaces as a value of safewriter.FaultVariable makes Find ignore the standard
// install places. It exists for the tests: they cannot take KeePassXC off a system, and a test on
// a system with KeePassXC could not see what happens without it.
const FaultNoInstallPlaces = "no-install-places"

// Program is a keepassxc-cli that can judge a result.
type Program struct {
	Path string
}

const (
	programName = "keepassxc-cli"
	// The format of a result (KDBX 4.1) and attachment-export both came with KeePassXC 2.7.
	minimumMajor = 2
	minimumMinor = 7
	// versionTimeout ends the question to a program that hangs.
	versionTimeout = 30 * time.Second
)

// Find looks for keepassxc-cli on PATH and then in the standard install places: a person who
// runs this tool from a flash drive may have KeePassXC installed without having it on PATH. The
// program gets the password, so a file that only happens to lie in the current directory does not
// count, even when PATH or the system asks for that.
func Find() (Program, error) {
	path := locate()
	if path == "" {
		return Program{}, ErrNotFound
	}
	version, err := versionOf(path)
	if err != nil {
		return Program{}, &UnusableError{Path: path}
	}
	if tooOld(version) {
		return Program{}, &UnusableError{Path: path, Version: version}
	}
	return Program{Path: path}, nil
}

func locate() string {
	// LookPath gives an error for a result from the current directory.
	if path, err := exec.LookPath(programName); err == nil && takesArgumentsAsTheyAre(path) {
		if absolute, err := filepath.Abs(path); err == nil {
			return absolute
		}
	}
	if safewriter.FaultIsOn(FaultNoInstallPlaces) {
		return ""
	}
	for _, place := range installPlaces() {
		if info, err := os.Stat(place); err == nil && info.Mode().IsRegular() {
			return place
		}
	}
	return ""
}

// takesArgumentsAsTheyAre rules out a script on Windows. A .cmd or a .bat file runs through cmd.exe,
// and that reads its arguments as a command. The arguments hold titles from the database, so a
// crafted title would run as a command.
func takesArgumentsAsTheyAre(path string) bool {
	return runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(path), ".exe")
}

// installPlaces gives where the installers of KeePassXC put the program on this system. Snap,
// Flatpak and AppImage have no place like that, and a confined program could not read a file on a
// flash drive anyway. A person with one of those can add the program to PATH.
func installPlaces() []string {
	switch runtime.GOOS {
	case "windows":
		const inProgramFiles = `KeePassXC\keepassxc-cli.exe`
		var places []string
		for _, variable := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if directory := os.Getenv(variable); directory != "" {
				places = append(places, filepath.Join(directory, inProgramFiles))
			}
		}
		return append(places, `C:\Program Files\`+inProgramFiles)
	case "darwin":
		const inBundle = "KeePassXC.app/Contents/MacOS/keepassxc-cli"
		places := []string{"/Applications/" + inBundle}
		if home, err := os.UserHomeDir(); err == nil {
			places = append(places, filepath.Join(home, "Applications", inBundle))
		}
		return append(places, "/opt/homebrew/bin/keepassxc-cli", "/usr/local/bin/keepassxc-cli")
	default:
		return []string{"/usr/bin/keepassxc-cli", "/usr/local/bin/keepassxc-cli"}
	}
}

func versionOf(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

var versionNumbers = regexp.MustCompile(`^(\d+)\.(\d+)`)

// tooOld knows only a version it can read. A text it cannot read does not take the check away:
// a program of an unknown kind can still fail the check, and then the person sees that.
func tooOld(version string) bool {
	numbers := versionNumbers.FindStringSubmatch(version)
	if numbers == nil {
		return false
	}
	major, errMajor := strconv.Atoi(numbers[1])
	minor, errMinor := strconv.Atoi(numbers[2])
	if errMajor != nil || errMinor != nil {
		return false
	}
	return major < minimumMajor || (major == minimumMajor && minor < minimumMinor)
}
