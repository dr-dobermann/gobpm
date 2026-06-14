package thresher

import "runtime/debug"

// version is the engine's semantic version. It may be overridden at build time
// with -ldflags "-X github.com/dr-dobermann/gobpm/pkg/thresher.version=v1.2.3".
// When left empty the toolchain-embedded module version is used instead.
var version = "v0.1.1"

// banner is the engine wordmark printed at startup, one log record per line. A
// raw literal keeps the backslashes literal; the lone backtick on line 3 is
// spliced in as an interpreted segment (a raw literal cannot contain one).
const banner = `           ___ ___ __  __
  __ _ ___| _ ) _ \  \/  |
 / _` + "`" + ` / _ \ _ \  _/ |\/| |
 \__, \___/___/_| |_|  |_|
 |___/`

// separator closes the startup block, setting the banner and resolved
// configuration apart from the application log that follows.
const separator = "────────────────────────────────────────────────────────────"

// buildInfo holds the version-control metadata baked into the binary by the Go
// toolchain (the vcs.* build settings, present for builds made from a repo).
type buildInfo struct {
	version  string
	revision string
	revTime  string
	modified bool
}

// readBuildInfo resolves the engine version and the last commit recorded in the
// binary. ReadBuildInfo yields a nil info when build data is unavailable (e.g. a
// build outside a git tree); buildInfoFrom degrades that to "unknown" rather
// than failing, so startup logging never blocks New.
func readBuildInfo() buildInfo {
	info, _ := debug.ReadBuildInfo()

	return buildInfoFrom(version, info)
}

// buildInfoFrom derives the build metadata from a version string and the
// toolchain build info. It is the pure, testable core of readBuildInfo: a nil
// info, or absent vcs.* settings, resolve to "unknown".
func buildInfoFrom(ver string, info *debug.BuildInfo) buildInfo {
	bi := buildInfo{
		version:  ver,
		revision: "unknown",
		revTime:  "unknown",
	}

	if info == nil {
		return bi
	}

	if bi.version == "" && info.Main.Version != "" {
		bi.version = info.Main.Version
	}

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			bi.revision = s.Value
		case "vcs.time":
			bi.revTime = s.Value
		case "vcs.modified":
			bi.modified = s.Value == "true"
		}
	}

	return bi
}

// shortRevision returns the abbreviated commit hash, suffixed with "-dirty" when
// the working tree carried uncommitted changes at build time.
func (b buildInfo) shortRevision() string {
	rev := b.revision
	if len(rev) > 7 {
		rev = rev[:7]
	}

	if b.modified {
		rev += "-dirty"
	}

	return rev
}
