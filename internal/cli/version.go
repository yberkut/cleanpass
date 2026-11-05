package cli

import (
	"runtime/debug"

	"github.com/yberkut/cleanpass/internal/text"
)

// versionLine gives what --version prints: the version that go build wrote into the file, as it is.
// A build without the data of git writes "(devel)". A file without build info, or with an empty
// version, has no version to give either.
func versionLine(info *debug.BuildInfo, ok bool) string {
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return text.VersionUnknown
	}
	return info.Main.Version
}
