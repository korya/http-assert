package main_test

import "testing"

// TestE2EStatusSpec covers the forms --assert-status accepts end to end, and
// the rejection that keeps a typo out of the assertion exit code (#93).
func TestE2EStatusSpec(t *testing.T) {
	// /ok answers 200, /created 201, /500 500.
	t.Run("an exact code still works", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-status", "200", url("/ok")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "200", url("/500")), exitAssertFail)
	})

	t.Run("a class matches its hundred", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-status", "2xx", url("/ok")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "2xx", url("/created")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "2xx", url("/500")), exitAssertFail)
		assertExit(t, run(t, nil, "--assert-status", "5xx", url("/500")), exitOK)
	})

	t.Run("a list matches any of its entries", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-status", "200,201", url("/ok")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "200,201", url("/created")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "204,301", url("/ok")), exitAssertFail)
	})

	t.Run("a range matches its span, inclusive at both ends", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-status", "200-201", url("/ok")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "200-201", url("/created")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "201-300", url("/ok")), exitAssertFail)
	})

	t.Run("the forms mix in one spec", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-status", "301,2xx,500-503", url("/ok")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "301,2xx,500-503", url("/500")), exitOK)
		assertExit(t, run(t, nil, "--assert-status", "301,404,410-418", url("/ok")), exitAssertFail)
	})

	// The failure quotes the spec as written rather than an expansion of it.
	t.Run("the failure names the spec the caller wrote", func(t *testing.T) {
		r := run(t, nil, "--assert-status", "4xx", url("/ok"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `status: expected 4xx, got 200 ("200 OK")`)

		r = run(t, nil, "--assert-status", "301,410-418", url("/ok"))
		assertContains(t, r, `status: expected 301,410-418, got 200 ("200 OK")`)
	})

	// A spec no response can satisfy is a typo, so it must not reach exit 93 --
	// a CI job reading that code would conclude the service was broken (#93).
	t.Run("an unusable spec is rejected before the request", func(t *testing.T) {
		for _, tc := range []struct{ Spec, Want string }{
			{"-1", "not a three-digit status code"},
			{"1000", "not a three-digit status code"},
			{"099", "no response can carry status"},
			{"0xx", "not a status class"},
			{"403-401", "counts down"},
			{"200,,204", "empty entry"},
			{"nonsense", "not a three-digit status code"},
		} {
			r := run(t, nil, "--assert-status", tc.Spec, url("/ok"))
			assertExit(t, r, exitBadInvocation)
			assertContains(t, r, "Invalid value for --assert-status flag")
			assertContains(t, r, tc.Want)
		}
	})

	// Rejection happens at the flag, so no request is made at all.
	t.Run("a rejected spec is still rejected against an unreachable host", func(t *testing.T) {
		r := run(t, nil, "--assert-status", "1000", "http://127.0.0.1:9/never")
		assertExit(t, r, exitBadInvocation)
	})

	t.Run("it is still single-valued", func(t *testing.T) {
		r := run(t, nil, "--assert-status", "200", "--assert-status", "2xx", url("/ok"))
		assertExit(t, r, exitBadInvocation)
		assertContains(t, r, "accepts a single value")
	})
}
