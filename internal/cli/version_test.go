package cli_test

import (
	"runtime/debug"
	"testing"

	"github.com/yberkut/cleanpass/internal/cli"
	"github.com/yberkut/cleanpass/internal/text"
)

func buildInfo(version string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Path: "github.com/yberkut/cleanpass", Version: version}}
}

// The line is the tool version as go build wrote it, with nothing cut off and nothing added.
func TestToolVersionIsWhatGoBuildWrote(t *testing.T) {
	for _, version := range []string{
		"v1.2.3",
		"v0.1.1-0.20261105123736-92029b4e4730",
		"v0.1.1-0.20261105123736-92029b4e4730+dirty",
		"v1.2.3+dirty",
	} {
		if got := cli.VersionLine(buildInfo(version), true); got != version {
			t.Errorf("version %q gives %q", version, got)
		}
	}
}

// A file without the data of git has no tool version. The person gets words, not an empty line or
// "(devel)".
func TestNoToolVersionSaysItIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
	}{
		{"built without the data of git", buildInfo("(devel)"), true},
		{"an empty version", buildInfo(""), true},
		{"no build info", nil, false},
	} {
		if got := cli.VersionLine(tc.info, tc.ok); got != text.VersionUnknown {
			t.Errorf("%s: %q, want the text about an unknown tool version", tc.name, got)
		}
	}
}
