package main_test

import "testing"

// This file pins behaviour that is currently WRONG. Each test asserts what the
// tool does today and names the issue tracking the defect.
//
// These are not skipped. When an issue is fixed the corresponding test fails,
// which is the signal to update the expectation in the same commit that lands
// the fix. Select them all with:
//
//	go test -run 'TestKnown' ./...

// TestIssue17BadPatternIsRejected pins that an unparseable pattern is reported
// as an invalid flag value rather than reaching the runtime as a panic.
//
// This test previously asserted the panic. It flipped when #17 was fixed, which
// is the record of that change.
func TestIssue17BadPatternIsRejected(t *testing.T) {
	for _, tc := range []struct {
		Flag string
		Arg  string
	}{
		{"--assert-body", "[unclosed"},
		{"--assert-header", "X-Any: (unclosed"},
		{"--assert-redirect", "[unclosed"},
	} {
		t.Run(tc.Flag, func(t *testing.T) {
			r := run(t, nil, tc.Flag, tc.Arg, url("/ok"))

			assertExit(t, r, exitBadFlagVal)
			// The flag at fault is named, and the parser's reason survives.
			assertContains(t, r, "Invalid value for "+tc.Flag+" flag")
			assertContains(t, r, "error parsing regexp")
			assertNotContains(t, r, "panic:")
			assertNotContains(t, r, "goroutine")
		})
	}

	// A pattern that compiles is unaffected.
	t.Run("valid patterns still work", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body", `"status":\s*"success"`, url("/ok")), exitOK)
	})
}

// TestKnownIssue20AssertOkAcceptsRedirects: --assert-ok is documented as "2xx"
// but implemented as 200-399.
func TestKnownIssue20AssertOkAcceptsRedirects(t *testing.T) {
	for _, path := range []string{"/redirect", "/redirect-rel"} {
		t.Run(path, func(t *testing.T) {
			characterizes(t, 20, "--assert-ok passes on a 3xx despite the docs saying 2xx")
			assertExit(t, run(t, nil, "--assert-ok", url(path)), exitOK)
		})
	}
}

// TestKnownIssue23WildcardMaphostUnreachable: hostMapping.Matches handles "*"
// and "*:*", but the parser rejects both, so the branches are dead code.
func TestKnownIssue23WildcardMaphostUnreachable(t *testing.T) {
	for _, src := range []string{"*", "*:*"} {
		t.Run(src, func(t *testing.T) {
			characterizes(t, 23, "the parser rejects a wildcard the matcher supports")

			r := run(t, nil, "--maphost", src+"="+hostPort(), "--assert-ok", "http://mapped.invalid/ok")
			assertExit(t, r, exitBadFlagVal)
			assertContains(t, r, "Invalid value for --maphost flag")
		})
	}
}

// TestKnownIssue25NoAssertionsIsNotAUsageError: the condition is detected before
// any request is made, yet reported with the transport-failure code.
func TestKnownIssue25NoAssertionsIsNotAUsageError(t *testing.T) {
	characterizes(t, 25, "a usage error is reported as 'Cannot perform request' with exit 93")

	r := run(t, nil, url("/ok"))
	assertExit(t, r, exitRequestFail) // ought to be exitUsage
	assertContains(t, r, "Cannot perform request: no assertions defined")
}

// TestKnownIssue26MaphostErrorDiscarded: parseHostMappings produces a precise
// message that mustParseHostMappings throws away.
func TestKnownIssue26MaphostErrorDiscarded(t *testing.T) {
	characterizes(t, 26, "the specific parse error is replaced by the raw flag slice")

	r := run(t, nil, "--maphost", "garbage", "--assert-ok", url("/ok"))
	assertExit(t, r, exitBadFlagVal)
	assertContains(t, r, "Invalid value for --maphost flag: [garbage]")
	assertNotContains(t, r, "has no separator")
}

// TestIssue28DataImpliesPost pins that -d implies POST and curl's default
// Content-Type, with -X and an explicit Content-Type header overriding.
//
// This test previously asserted that the method stayed GET. It flipped when
// #28 was fixed, which is the record of that change.
func TestIssue28DataImpliesPost(t *testing.T) {
	for _, tc := range []struct {
		Name    string
		Args    []string
		Want    []string
		NotWant []string
	}{
		{
			Name: "bare -d implies POST and form Content-Type",
			Args: []string{"-d", "hello=1"},
			Want: []string{`\"method\":\"POST\"`, `\"body\":\"hello=1\"`,
				"application/x-www-form-urlencoded"},
		},
		{
			// The Content-Type follows -d, not the implied method, as in curl.
			Name: "-X wins over -d, keeping the form Content-Type",
			Args: []string{"-X", "PUT", "-d", "hello=1"},
			Want: []string{`\"method\":\"PUT\"`, "application/x-www-form-urlencoded"},
		},
		{
			Name: "explicit -X GET wins even against the default",
			Args: []string{"-X", "GET", "-d", "hello=1"},
			Want: []string{`\"method\":\"GET\"`},
		},
		{
			Name:    "an explicit Content-Type is not replaced",
			Args:    []string{"-d", "{}", "-H", "Content-Type: application/json"},
			Want:    []string{`\"method\":\"POST\"`, "application/json"},
			NotWant: []string{"application/x-www-form-urlencoded"},
		},
		{
			Name: "empty -d still posts an empty body, with the form Content-Type",
			Args: []string{"-d", ""},
			Want: []string{`\"method\":\"POST\"`, `\"body\":\"\"`,
				"application/x-www-form-urlencoded"},
		},
		{
			Name:    "without -d nothing changes: GET, no Content-Type",
			Args:    nil,
			Want:    []string{`\"method\":\"GET\"`},
			NotWant: []string{"application/x-www-form-urlencoded"},
		},
		{
			// The suppression idiom from the README: an empty Content-Type
			// counts as given, so the form default must not overwrite it.
			Name:    "empty Content-Type header suppresses the default",
			Args:    []string{"-d", "hello=1", "-H", "Content-Type:"},
			Want:    []string{`\"method\":\"POST\"`},
			NotWant: []string{"application/x-www-form-urlencoded"},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			args := append(tc.Args, "--assert-body-eq", "never-matches", url("/echo"))
			r := run(t, nil, args...)
			for _, want := range tc.Want {
				assertContains(t, r, want)
			}
			for _, notWant := range tc.NotWant {
				assertNotContains(t, r, notWant)
			}
		})
	}

	// The implied POST goes through the same replay machinery as an explicit
	// one; these mirror the -X POST cases in the redirect and retry suites.
	t.Run("308 replays the implied method and body", func(t *testing.T) {
		r := run(t, nil, "-L", "-d", "replayed-payload",
			"--assert-body", `"body":"replayed-payload".*"method":"POST"`,
			url("/redirect-308"))
		assertExit(t, r, exitOK)
	})

	t.Run("the implied POST body survives a retry", func(t *testing.T) {
		r := run(t, nil, "--retry", "3", "--retry-delay", "50ms",
			"-d", "replayed-payload",
			"--assert-body", `"body":"replayed-payload".*"method":"POST"`,
			flaky(t, "/flaky-echo", 2))
		assertExit(t, r, exitOK)
	})
}

// TestKnownIssue31MaxTimeAcceptsNonPositive: values <= 0 reach http.Client.Timeout,
// where they mean "no timeout" rather than being rejected.
func TestKnownIssue31MaxTimeAcceptsNonPositive(t *testing.T) {
	for _, v := range []string{"0", "-5"} {
		t.Run("max-time "+v, func(t *testing.T) {
			characterizes(t, 31, "a non-positive --max-time silently disables the timeout")
			assertExit(t, run(t, nil, "-m", v, "--assert-ok", url("/ok")), exitOK)
		})
	}
}

// TestKnownIssue34StdoutAlwaysEmpty: every byte goes to stderr, so redirecting
// stdout captures nothing.
func TestKnownIssue34StdoutAlwaysEmpty(t *testing.T) {
	characterizes(t, 34, "results are on stderr; stdout is never written to")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"on success", []string{"--assert-ok", url("/ok")}},
		{"on failure", []string{"--assert-ok", url("/500")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, nil, tc.args...)
			if r.Stdout != "" {
				t.Fatalf("stdout is no longer empty (%q); #34 is fixed -- update this test", r.Stdout)
			}
			if r.Stderr == "" {
				t.Fatal("stderr unexpectedly empty")
			}
		})
	}
}

// TestKnownIssueWarnLevelIsDead: nothing logs at warn, so --log-level warn is
// byte-identical to --log-level error. The unused logWarn/logError helpers have
// been removed; the LWarn level itself stays, because --log-level warn is part
// of the documented flag surface.
func TestKnownIssueWarnLevelIsDead(t *testing.T) {
	characterizes(t, 0, "--log-level warn behaves exactly like error; nothing logs at warn")

	warn := run(t, nil, "--log-level", "warn", "--assert-ok", url("/ok"))
	fail := run(t, nil, "--log-level", "error", "--assert-ok", url("/ok"))

	if warn.Output() != fail.Output() {
		t.Fatalf("warn and error now differ; the level gained meaning -- update this test\nwarn: %q\nerror: %q",
			warn.Output(), fail.Output())
	}
	assertExit(t, warn, exitOK)
}
