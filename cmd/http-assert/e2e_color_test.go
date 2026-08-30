package main_test

import (
	"strings"
	"testing"
)

const esc = "\033["

// TestE2EColor covers what a caller can observe about colour (#98).
//
// The end-to-end suite captures output through a pipe, which is exactly the
// condition --color=auto exists to detect -- so the default staying plain here
// is the CI-log guarantee being tested, not an accident of the harness.
func TestE2EColor(t *testing.T) {
	t.Run("the default is plain when output is not a terminal", func(t *testing.T) {
		for _, u := range []string{url("/ok"), url("/500")} {
			r := run(t, nil, "--assert-ok", u)
			if strings.Contains(r.Output(), esc) {
				t.Errorf("piped output carries ANSI: %q", r.Output())
			}
		}
	})

	t.Run("--color=never is plain", func(t *testing.T) {
		r := run(t, nil, "--color=never", "--assert-ok", url("/500"))
		assertExit(t, r, exitAssertFail)
		if strings.Contains(r.Output(), esc) {
			t.Errorf("--color=never carries ANSI: %q", r.Output())
		}
	})

	t.Run("--color=always colours the verdict even in a pipe", func(t *testing.T) {
		pass := run(t, nil, "--color=always", "--assert-ok", url("/ok"))
		assertExit(t, pass, exitOK)
		assertContains(t, pass, "\033[32m[+] PASSED")

		fail := run(t, nil, "--color=always", "--assert-ok", url("/500"))
		assertExit(t, fail, exitAssertFail)
		assertContains(t, fail, "\033[31m[-] FAILED")
		assertContains(t, fail, "\033[31mError:\033[0m")
	})

	t.Run("--color=always dims the trace lines", func(t *testing.T) {
		r := run(t, nil, "--color=always", "--assert-ok", url("/ok"))
		assertContains(t, r, "\033[2m[.] ")
		assertContains(t, r, "\033[2m[:] ")
	})

	// The failure list is copied out of terminals; escapes in it would travel.
	t.Run("the assertion lines stay plain even with colour on", func(t *testing.T) {
		r := run(t, nil, "--color=always", "--assert-status", "200", url("/500"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "\n- status: expected 200, got 500")
	})

	// NO_COLOR only changes the answer when stderr is a terminal, and this
	// harness pipes -- so "auto plus NO_COLOR is plain" would pass here even
	// if NO_COLOR were ignored entirely. That branch is covered by
	// Test_shouldColor, which takes the terminal as an argument instead.
	//
	// This one is observable: if NO_COLOR wrongly won, the output would be
	// plain. A variable says what to do absent an instruction; the flag is one.
	t.Run("--color=always overrides NO_COLOR", func(t *testing.T) {
		r := run(t, map[string]string{"NO_COLOR": "1"}, "--color=always", "--assert-ok", url("/ok"))
		assertContains(t, r, "\033[32m[+] PASSED")
	})

	// A retry is the one trace line reporting trouble that is not the verdict,
	// so it is neither dimmed with the rest of the trace nor red like a
	// failure: a run that passed on the fourth attempt is not the same news as
	// one that passed on the first.
	t.Run("--color=always makes the retry line yellow", func(t *testing.T) {
		r := run(t, nil, "--color=always", "--retry", "2", "--retry-delay", "10ms",
			"--assert-ok", url("/500"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "\033[33m[~] retry 1/2")
		assertNotContains(t, r, "\033[2m[~]")

		// The verdict it leads to is still red, and still distinguishable.
		assertContains(t, r, "\033[31m[-] FAILED")
	})

	t.Run("a retry that recovers still colours the retry yellow", func(t *testing.T) {
		r := run(t, nil, "--color=always", "--retry", "3", "--retry-delay", "10ms",
			"--assert-ok", flaky(t, "/flaky", 1))
		assertExit(t, r, exitOK)
		assertContains(t, r, "\033[33m[~] retry 1/3")
		assertContains(t, r, "\033[32m[+] PASSED")
	})

	t.Run("an unknown value is rejected", func(t *testing.T) {
		r := run(t, nil, "--color=purple", "--assert-ok", url("/ok"))
		assertExit(t, r, exitBadInvocation)
		assertContains(t, r, "Invalid value for --color flag")
		assertContains(t, r, "auto, always, never")
	})
}
