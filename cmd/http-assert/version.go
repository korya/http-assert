package main

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// version is the release identity, stamped by goreleaser through
// -ldflags "-X main.version=v<tag>". Every other way of obtaining this binary
// leaves it empty and the identity is recovered from build information instead.
var version = ""

// versionString is what --version prints.
func versionString() string {
	bi, _ := debug.ReadBuildInfo()
	return buildVersion(version, bi)
}

// buildVersion describes the binary from the release stamp and from the build
// information Go embeds, preferring the stamp when both are available.
//
// The stamp on its own is not enough. Only the release build sets it, so
// `go install ...@latest` -- the installation the README leads with -- would
// report nothing at all. Go already knows the answer there: it records the
// module version for a module install, and a pseudo-version plus the commit
// and its timestamp for a build from a checkout.
//
// A field with nothing behind it is omitted rather than printed empty, which
// is why a release names a commit and a module install does not. For a build
// from a checkout the pseudo-version and the commit do overlap; that is
// deliberate, because reading a seven-character revision beats picking one out
// of a pseudo-version. bi is nil when the binary carries no build information.
func buildVersion(stamped string, bi *debug.BuildInfo) string {
	v, goVersion := stamped, runtime.Version()
	var commit, built string

	if bi != nil {
		if v == "" {
			v = bi.Main.Version
		}
		if bi.GoVersion != "" {
			goVersion = bi.GoVersion
		}

		settings := make(map[string]string, len(bi.Settings))
		for _, s := range bi.Settings {
			settings[s.Key] = s.Value
		}
		if commit = settings["vcs.revision"]; commit != "" {
			if len(commit) > shortRevisionLen {
				commit = commit[:shortRevisionLen]
			}
			if settings["vcs.modified"] == "true" {
				commit += "-dirty"
			}
		}
		built = settings["vcs.time"]
	}

	if v == "" {
		v = "unknown"
	}

	fields := make([]string, 0, 4)
	if commit != "" {
		fields = append(fields, "commit "+commit)
	}
	if built != "" {
		fields = append(fields, "built "+built)
	}
	fields = append(fields, goVersion, runtime.GOOS+"/"+runtime.GOARCH)

	return v + " (" + strings.Join(fields, ", ") + ")"
}

// shortRevisionLen is the git convention for an abbreviated revision.
const shortRevisionLen = 7
