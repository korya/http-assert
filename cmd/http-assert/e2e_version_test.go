package main_test

import (
	"regexp"
	"strings"
	"testing"
)

// The identity of the binary is the first thing anyone asks of a pinned CI
// tool, so --version is part of the contract: it answers on stdout, it exits
// zero, and it does so without being given a URL.
//
// The shape is pinned rather than the content. The suite builds the CLI from
// whatever checkout it is running in, so the version is a release tag in a
// release, a pseudo-version in a clone, and "(devel)" wherever Go stamps no
// version control information at all. Two ways to meet that last one: `go run`,
// which never stamps -- only `go build` and `go install` ask for it -- and a
// linked git worktree, where .git is a file and Go stops looking.
func versionLine(t *testing.T) *regexp.Regexp {
	t.Helper()

	re, err := regexp.Compile(
		`^http-assert version \S+ \(.*go1\.[0-9.]+, [a-z0-9]+/[a-z0-9]+\)$`)
	if err != nil {
		t.Fatalf("bad version-line pattern: %s", err)
	}
	return re
}

func TestE2EVersion(t *testing.T) {
	want := versionLine(t)

	t.Run("reports an identity on stdout", func(t *testing.T) {
		r := run(t, nil, "--version")
		assertExit(t, r, exitOK)

		got := strings.TrimSpace(r.Stdout)
		if !want.MatchString(got) {
			t.Fatalf("--version = %q, want a match for %s", got, want)
		}
		if r.Stderr != "" {
			t.Fatalf("--version wrote to stderr: %q", r.Stderr)
		}
	})

	// --version is asked before the URL argument is required. A tool that
	// demanded its normal arguments to identify itself would be useless in the
	// place the question is actually asked: a broken pipeline.
	t.Run("needs no URL", func(t *testing.T) {
		bare := run(t, nil)
		if bare.ExitCode == exitOK {
			t.Fatal("a bare invocation succeeded; the URL is supposed to be required")
		}
		assertExit(t, run(t, nil, "--version"), exitOK)
	})

	// -v belongs to --verbose and must keep belonging to it; cobra hands the
	// shorthand to --version whenever it finds it free.
	t.Run("does not take the -v shorthand", func(t *testing.T) {
		r := run(t, nil, "-v", "--assert-ok", url("/ok"))
		assertExit(t, r, exitOK)
		if want.MatchString(strings.TrimSpace(r.Stdout)) {
			t.Fatalf("-v printed the version instead of enabling verbose logging\n%s", r.Output())
		}
	})
}
