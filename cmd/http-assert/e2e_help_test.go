package main_test

import (
	"fmt"
	"strings"
	"testing"
)

// --help is the only reference available at the terminal, and the two things
// this tool's callers most need from it -- what the exit code will be, and
// what can be set without touching the command line -- were the two things it
// never mentioned (#39).
//
// The exit codes live in four places now: here, the README, the package
// comment, and the constants in the harness. Prose cannot be generated from
// the constants, so this test does the next best thing and fails when they
// stop agreeing.

func TestE2EHelpDocumentsTheContract(t *testing.T) {
	r := run(t, nil, "--help")
	assertExit(t, r, exitOK)

	t.Run("every exit code the CLI can return is listed", func(t *testing.T) {
		for _, code := range []int{exitOK, exitBadInvocation, exitTransportFail, exitAssertFail} {
			if !strings.Contains(r.Output(), fmt.Sprintf("\n  %d ", code)) {
				t.Errorf("--help does not document exit code %d", code)
			}
		}
	})

	// Exit 2 was the Go panic path (#61); 91 and 103 merged into the
	// invocation code when the categories shrank to three. All are
	// deliberately absent rather than overlooked.
	t.Run("retired exit codes are not advertised", func(t *testing.T) {
		for _, code := range []int{2, 91, 103} {
			if strings.Contains(r.Output(), fmt.Sprintf("\n  %d ", code)) {
				t.Errorf("--help documents exit code %d, which the CLI can no longer return", code)
			}
		}
	})

	t.Run("every environment-backed option is named", func(t *testing.T) {
		for _, tc := range envSupportedCases(t) {
			if !strings.Contains(r.Output(), tc.EnvKey) {
				t.Errorf("--help does not mention %s", tc.EnvKey)
			}
		}
	})

	// Proxying works through the transport's environment defaults and has no
	// flag, so --help is the only place a user could learn it exists (#46).
	t.Run("proxy support is discoverable", func(t *testing.T) {
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
			if !strings.Contains(r.Output(), name) {
				t.Errorf("--help does not mention %s", name)
			}
		}
	})

	// The flag list is found by cutting the help output at "Flags:", so that
	// string appearing in the prose above would silently point the parser at
	// the wrong section -- and a probe that reads no flags passes.
	t.Run("prose does not shadow the flag section", func(t *testing.T) {
		if n := strings.Count(r.Output(), "Flags:"); n != 1 {
			t.Fatalf("help output contains %d occurrences of \"Flags:\", want exactly 1", n)
		}
	})
}
