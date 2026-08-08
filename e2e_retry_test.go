package main_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// --retry is the only option that makes the tool wait rather than answer, so
// what it does is worth pinning precisely: the default it departs from, what
// counts as a failure worth another attempt, the two bounds on the loop, and
// the combinations it refuses.
//
// Every delay used here is small but never below 50ms: the end-to-end suite
// runs on Windows in CI, whose timer granularity is coarse enough to make
// tighter waits arrive early.

// flaky builds a URL for an endpoint that fails the first n requests and then
// recovers. The id keeps one test's counter away from every other test's, which
// matters because the counter lives in this process while the attempts are made
// by a subprocess.
func flaky(t *testing.T, path string, n int) string {
	t.Helper()
	return url(fmt.Sprintf("%s?id=%s&fail=%d", path, t.Name(), n))
}

// retries counts the lines the CLI logs between attempts, which is the only
// externally visible record of how many were made.
func retries(r result) int { return strings.Count(r.Output(), "[~] retry ") }

// TestE2ERetryDefaultIsASingleAttempt pins the behaviour --retry opts out of.
func TestE2ERetryDefaultIsASingleAttempt(t *testing.T) {
	// One failure is all it takes, and the endpoint would have recovered on the
	// very next request. Without --retry there is no next request.
	r := run(t, nil, "--assert-ok", flaky(t, "/flaky", 1))
	assertExit(t, r, exitAssertFail)
	if n := retries(r); n != 0 {
		t.Fatalf("retried %d times with --retry unset\n%s", n, r.Output())
	}

	// And the failure reads exactly as it did before retrying existed: no
	// attempt count, because there was nothing to count.
	assertNotContains(t, r, "gave up after")
}

func TestE2ERetryRecovers(t *testing.T) {
	// The case the feature exists for: the service is not up yet, so the
	// response arrives perfectly well and says the wrong thing.
	t.Run("an assertion failure is retried", func(t *testing.T) {
		r := run(t, nil, "--retry", "3", "--retry-delay", "50ms",
			"--assert-ok", flaky(t, "/flaky", 2))
		assertExit(t, r, exitOK)
		if n := retries(r); n != 2 {
			t.Fatalf("retried %d times, want 2\n%s", n, r.Output())
		}
	})

	// The other half of "any failure": nothing answered at all.
	t.Run("a transport failure is retried", func(t *testing.T) {
		r := run(t, nil, "--retry", "3", "--retry-delay", "50ms",
			"--assert-ok", flaky(t, "/flaky-hangup", 2))
		assertExit(t, r, exitOK)
		if n := retries(r); n != 2 {
			t.Fatalf("retried %d times, want 2\n%s", n, r.Output())
		}
	})

	// Retrying stops at the first attempt that passes; it does not run the
	// budget down for the sake of it.
	t.Run("a first-attempt success costs no delay", func(t *testing.T) {
		start := time.Now()
		r := run(t, nil, "--retry", "5", "--retry-delay", "10s", "--assert-ok", url("/ok"))
		assertExit(t, r, exitOK)
		if n := retries(r); n != 0 {
			t.Fatalf("retried %d times after a passing attempt\n%s", n, r.Output())
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Fatalf("took %s; the delay was waited out despite the pass", d)
		}
	})

	// The request is sent again, not merely the URL. http.Client consumes the
	// body, so an attempt built from the same request twice would POST nothing
	// the second time -- and the endpoint would answer 200 to it, hiding the
	// bug behind a passing run.
	t.Run("the body survives being sent again", func(t *testing.T) {
		r := run(t, nil, "--retry", "3", "--retry-delay", "50ms",
			"-X", "POST", "-d", "replayed-payload",
			"--assert-body", `"body":"replayed-payload".*"method":"POST"`,
			flaky(t, "/flaky-echo", 2))
		assertExit(t, r, exitOK)
		if n := retries(r); n != 2 {
			t.Fatalf("retried %d times, want 2\n%s", n, r.Output())
		}
	})
}

// TestE2ERetryExhaustion covers the run that never recovers. The count is the
// part worth reporting: a log showing one failure and no sign of the other five
// reads as a service that was never up, rather than one that never came up.
func TestE2ERetryExhaustion(t *testing.T) {
	r := run(t, nil, "--retry", "2", "--retry-delay", "50ms", "--assert-ok", url("/500"))
	assertExit(t, r, exitAssertFail)

	// Two retries is three attempts, not two.
	assertContains(t, r, "gave up after 3 attempts")
	if n := retries(r); n != 2 {
		t.Fatalf("retried %d times, want 2\n%s", n, r.Output())
	}

	// The last failure is still reported in full underneath the summary.
	assertContains(t, r, "ok: expected OK, got 500")

	t.Run("--retry 0 is the default spelled out", func(t *testing.T) {
		r := run(t, nil, "--retry", "0", "--assert-ok", url("/500"))
		assertExit(t, r, exitAssertFail)
		assertNotContains(t, r, "gave up after")
		if n := retries(r); n != 0 {
			t.Fatalf("retried %d times with --retry 0\n%s", n, r.Output())
		}
	})
}

// TestE2ERetryMaxTime covers the second bound. --retry alone can only be
// bounded by counting, which says nothing about how long the count will take.
func TestE2ERetryMaxTime(t *testing.T) {
	t.Run("the budget ends the run before the count does", func(t *testing.T) {
		start := time.Now()
		r := run(t, nil, "--retry", "1000", "--retry-delay", "100ms",
			"--retry-max-time", "1s", "--assert-ok", url("/500"))
		assertExit(t, r, exitAssertFail)

		// Which bound stopped it is the whole reason the message names one.
		assertContains(t, r, "--retry-max-time is 1s")

		// Generous on both sides: the point is that 1000 attempts did not
		// happen, not that exactly ten did.
		if n := retries(r); n == 0 || n > 100 {
			t.Fatalf("retried %d times under a 1s budget with a 100ms delay\n%s", n, r.Output())
		}
		if d := time.Since(start); d > 30*time.Second {
			t.Fatalf("took %s; the budget was not applied", d)
		}
	})

	// Zero is not "give up immediately". It is curl's meaning: no budget, so
	// only --retry bounds the loop.
	t.Run("zero means no budget", func(t *testing.T) {
		r := run(t, nil, "--retry", "2", "--retry-delay", "50ms",
			"--retry-max-time", "0", "--assert-ok", url("/500"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "gave up after 3 attempts")
		assertNotContains(t, r, "--retry-max-time is")
	})

	// A budget that outlasts the count leaves the count in charge.
	t.Run("a budget larger than the count is not reported", func(t *testing.T) {
		r := run(t, nil, "--retry", "1", "--retry-delay", "50ms",
			"--retry-max-time", "1m", "--assert-ok", url("/500"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "gave up after 2 attempts")
		assertNotContains(t, r, "--retry-max-time is")
	})
}

// TestE2ERetryMaxTimeIsPerAttempt: -m and --retry-max-time bound different
// things, and reading -m as a bound on the run would make every retry after the
// first impossible.
func TestE2ERetryMaxTimeIsPerAttempt(t *testing.T) {
	// /slow outlasts a 1s budget every time, so each attempt has to time out
	// on its own clock for the second one to happen at all.
	r := run(t, nil, "-m", "1", "--retry", "1", "--retry-delay", "50ms",
		"--assert-ok", url("/slow"))
	assertExit(t, r, exitTransportFail)

	if n := retries(r); n != 1 {
		t.Fatalf("retried %d times, want 1 -- -m was read as a bound on the run\n%s",
			n, r.Output())
	}
	assertContains(t, r, "gave up after 2 attempts")
	assertContains(t, r, "Client.Timeout")
}

// TestE2ERetryRejectedCombinations covers the invocations the CLI refuses to
// serve rather than resolve silently, on the same grounds as --max-redirs
// without -L: an option nobody will read, or a value that inverts its meaning.
func TestE2ERetryRejectedCombinations(t *testing.T) {
	for _, tc := range []struct {
		Name string
		Args []string
		Diag string
	}{
		{
			Name: "--retry-delay without --retry",
			Args: []string{"--retry-delay", "1s", "--assert-ok", url("/ok")},
			Diag: "Flag --retry-delay configures retrying that is not switched on",
		},
		{
			Name: "--retry-max-time without --retry",
			Args: []string{"--retry-max-time", "5s", "--assert-ok", url("/ok")},
			Diag: "Flag --retry-max-time configures retrying that is not switched on",
		},
		{
			Name: "a negative --retry",
			Args: []string{"--retry=-1", "--assert-ok", url("/ok")},
			Diag: "Invalid value for --retry flag: -1",
		},
		{
			// pflag accepts a negative duration without complaint, so nothing
			// but this check stands between it and a retry loop that never waits.
			Name: "a negative --retry-delay",
			Args: []string{"--retry", "1", "--retry-delay=-2s", "--assert-ok", url("/ok")},
			Diag: "Invalid value for --retry-delay flag: -2s",
		},
		{
			Name: "a negative --retry-max-time",
			Args: []string{"--retry", "1", "--retry-max-time=-2s", "--assert-ok", url("/ok")},
			Diag: "Invalid value for --retry-max-time flag: -2s",
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := run(t, nil, tc.Args...)
			assertExit(t, r, exitBadInvocation)
			assertContains(t, r, tc.Diag)
		})
	}

	// --retry 0 is how a script spells "no retries this time", so the durations
	// stay legal beside it. Refusing the pair would break `--retry ${N:-0}`.
	t.Run("--retry 0 still accepts the durations", func(t *testing.T) {
		r := run(t, nil, "--retry", "0", "--retry-delay", "1s",
			"--retry-max-time", "5s", "--assert-ok", url("/ok"))
		assertExit(t, r, exitOK)
	})

	// The durations are Go durations, so a bare number has no unit and is
	// rejected by the flag parser rather than guessed at.
	t.Run("a delay without a unit is refused", func(t *testing.T) {
		r := run(t, nil, "--retry", "1", "--retry-delay", "5", "--assert-ok", url("/ok"))
		assertExit(t, r, exitBadInvocation)
		assertContains(t, r, `missing unit in duration "5"`)
	})
}
