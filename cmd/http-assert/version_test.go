package main

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// platform is the tail every rendering ends with. It is built from the same
// constants buildVersion uses, so these tests stay honest on any host.
var platform = runtime.GOOS + "/" + runtime.GOARCH

func settings(kv ...string) []debug.BuildSetting {
	out := make([]debug.BuildSetting, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func Test_buildVersion(t *testing.T) {
	const (
		revision = "4ffe282a1b2c3d4e5f60718293a4b5c6d7e8f900"
		when     = "2026-08-07T22:24:59Z"
	)

	tests := []struct {
		Name    string
		Stamped string
		Info    *debug.BuildInfo
		Want    string
	}{{
		// What a release binary reports: the tag from the stamp, the commit
		// and timestamp from the checkout goreleaser built in.
		Name:    "release build",
		Stamped: "v0.1.0",
		Info: &debug.BuildInfo{
			Main:      debug.Module{Version: "(devel)"},
			GoVersion: "go1.25.5",
			Settings:  settings("vcs.revision", revision, "vcs.time", when, "vcs.modified", "false"),
		},
		Want: "v0.1.0 (commit 4ffe282, built " + when + ", go1.25.5, " + platform + ")",
	}, {
		// `go install github.com/korya/http-assert/cmd/http-assert@v0.0.7`.
		// The module version
		// is authoritative and there is no VCS information to report.
		Name: "module install",
		Info: &debug.BuildInfo{
			Main:      debug.Module{Version: "v0.0.7"},
			GoVersion: "go1.25.5",
		},
		Want: "v0.0.7 (go1.25.5, " + platform + ")",
	}, {
		// `go build` in a checkout with uncommitted changes. Go synthesises a
		// pseudo-version; the revision is marked so nobody mistakes the binary
		// for the commit it names.
		Name: "checkout build with local modifications",
		Info: &debug.BuildInfo{
			Main:      debug.Module{Version: "v0.0.8-0.20260807222459-4ffe282a1b2c+dirty"},
			GoVersion: "go1.25.5",
			Settings:  settings("vcs.revision", revision, "vcs.time", when, "vcs.modified", "true"),
		},
		Want: "v0.0.8-0.20260807222459-4ffe282a1b2c+dirty (commit 4ffe282-dirty, built " +
			when + ", go1.25.5, " + platform + ")",
	}, {
		// A revision already short enough is passed through untouched.
		Name: "revision shorter than the abbreviation",
		Info: &debug.BuildInfo{
			Main:      debug.Module{Version: "v0.0.7"},
			GoVersion: "go1.25.5",
			Settings:  settings("vcs.revision", "4ffe28"),
		},
		Want: "v0.0.7 (commit 4ffe28, go1.25.5, " + platform + ")",
	}, {
		// -buildvcs=false, or a build from an exported tree. Go reports the
		// module as unversioned and stamps nothing.
		Name: "no version control information",
		Info: &debug.BuildInfo{
			Main:      debug.Module{Version: "(devel)"},
			GoVersion: "go1.25.5",
		},
		Want: "(devel) (go1.25.5, " + platform + ")",
	}, {
		// Neither source knows anything. Saying so beats printing an empty
		// version that reads like a formatting bug.
		Name: "nothing known",
		Info: &debug.BuildInfo{},
		Want: "unknown (" + runtime.Version() + ", " + platform + ")",
	}, {
		// debug.ReadBuildInfo fails. The stamp still carries the release.
		Name:    "no build information, stamped",
		Stamped: "v0.1.0",
		Want:    "v0.1.0 (" + runtime.Version() + ", " + platform + ")",
	}, {
		Name: "no build information, unstamped",
		Want: "unknown (" + runtime.Version() + ", " + platform + ")",
	}}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			if got := buildVersion(tc.Stamped, tc.Info); got != tc.Want {
				t.Errorf("buildVersion() = %q, want %q", got, tc.Want)
			}
		})
	}
}

// Test_versionString guards the wiring rather than the formatting: whatever the
// test binary was built from, --version must have something to say.
func Test_versionString(t *testing.T) {
	if got := versionString(); got == "" {
		t.Fatal("versionString() is empty")
	}
}
