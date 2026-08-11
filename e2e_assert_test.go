package main_test

import "testing"

// TestE2EAssertions exercises every assertion option in both directions:
// a request that satisfies it, and one that does not. Passing only the happy
// case would not prove the assertion is doing any work.
func TestE2EAssertions(t *testing.T) {
	cases := []struct {
		Name string
		Args []string // assertion under test
		Pass string   // URL where it holds
		Fail string   // URL where it does not
		Diag string   // fragment the failure message must carry
	}{
		{"assert-ok", []string{"--assert-ok"}, url("/ok"), url("/500"), "ok: expected OK, got 500"},
		{"assert-status", []string{"--assert-status", "201"}, url("/created"), url("/ok"), "status: expected 201, got 200"},
		{"assert-header", []string{"--assert-header", `Cache-Control: max-age=\d+`}, url("/ok"), url("/created"), "header[Cache-Control]"},
		{"assert-header-eq", []string{"--assert-header-eq", "X-Api-Version: v1"}, url("/ok"), url("/created"), "header[X-Api-Version]"},
		{"assert-header-present", []string{"--assert-header-eq", "X-Api-Version"}, url("/ok"), url("/created"), "expected to be present"},
		{"assert-header-missing", []string{"--assert-header-missing", "X-Api-Version"}, url("/created"), url("/ok"), "expected to be missing"},
		{"assert-body", []string{"--assert-body", `"users":\s*\[\]`}, url("/ok"), url("/created"), "body: expected to match"},
		{"assert-body-eq", []string{"--assert-body-eq", "created"}, url("/created"), url("/ok"), "body: expected"},
		{"assert-body-empty", []string{"--assert-body-empty"}, url("/empty"), url("/ok"), "expected to be empty"},
		{"assert-redirect", []string{"--assert-redirect", `https://.*\.com/.*`}, url("/redirect"), url("/ok"), "redirect: wrong HTTP status"},
		{"assert-redirect-eq", []string{"--assert-redirect-eq", "https://new-domain.com/path"}, url("/redirect"), url("/redirect-rel"), "redirect: wrong Location"},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Run("holds", func(t *testing.T) {
				assertExit(t, run(t, nil, append(append([]string{}, tc.Args...), tc.Pass)...), exitOK)
			})

			t.Run("does not hold", func(t *testing.T) {
				r := run(t, nil, append(append([]string{}, tc.Args...), tc.Fail)...)
				assertExit(t, r, exitAssertFail)
				assertContains(t, r, tc.Diag)
			})
		})
	}
}

// TestE2EAssertionAggregation pins the behaviour that separates this tool from
// `curl && grep`: every failing assertion is reported, not just the first.
func TestE2EAssertionAggregation(t *testing.T) {
	r := run(t, nil,
		"--assert-ok",
		"--assert-status", "200",
		"--assert-header-eq", "X-Absent: 1",
		"--assert-body-eq", "not-boom",
		url("/500"))

	assertExit(t, r, exitAssertFail)
	assertContains(t, r, "4 assertions failed:")
	for _, want := range []string{"ok: expected OK", "status: expected 200", "header[X-Absent]", "body: expected"} {
		assertContains(t, r, want)
	}
}

// TestE2EExitCodes pins the process contract. Callers branch on these, and
// nothing in the repo verified them before this suite (#24).
func TestE2EExitCodes(t *testing.T) {
	cases := []struct {
		Name string
		Args []string
		Env  map[string]string
		Want int
		Diag string
	}{
		{Name: "success", Args: []string{"--assert-ok", url("/ok")}, Want: exitOK},
		{Name: "assertion failed", Args: []string{"--assert-ok", url("/500")}, Want: exitAssertFail, Diag: "assertions failed"},
		{Name: "connection refused", Args: []string{"--assert-ok", "http://127.0.0.1:9/"}, Want: exitTransportFail, Diag: "failed to send request"},
		{Name: "dns failure", Args: []string{"--assert-ok", "http://nonexistent.invalid/"}, Want: exitTransportFail, Diag: "failed to send request"},
		{Name: "unsupported scheme", Args: []string{"--assert-ok", "ftp://example.com/x"}, Want: exitTransportFail, Diag: "unsupported protocol scheme"},
		{Name: "tls verification", Args: []string{"--assert-ok", tlsSrv.URL}, Want: exitTransportFail, Diag: "certificate"},
		{Name: "invalid log level", Args: []string{"--log-level", "trace", "--assert-ok", url("/ok")}, Want: exitBadInvocation, Diag: "Invalid value for --log-level"},
		{Name: "invalid maphost", Args: []string{"--maphost", "garbage", "--assert-ok", url("/ok")}, Want: exitBadInvocation, Diag: "Invalid value for --maphost"},
		{Name: "invalid method", Args: []string{"-X", "BAD METHOD", "--assert-ok", url("/ok")}, Want: exitBadInvocation, Diag: "Cannot create request"},
		{Name: "malformed url", Args: []string{"--assert-ok", "ht!tp://[bad"}, Want: exitBadInvocation, Diag: "Cannot create request"},
		{Name: "no url", Args: []string{"--assert-ok"}, Want: exitBadInvocation, Diag: "accepts 1 arg(s)"},
		{Name: "too many urls", Args: []string{"--assert-ok", url("/ok"), url("/created")}, Want: exitBadInvocation, Diag: "accepts 1 arg(s)"},
		{Name: "unknown flag", Args: []string{"--nope", url("/ok")}, Want: exitBadInvocation, Diag: "unknown flag"},
		// Flipped by #25: detected before any request, so it is an invocation
		// error, not the transport failure it used to report.
		{Name: "no assertions", Args: []string{url("/ok")}, Want: exitBadInvocation, Diag: "No assertions specified"},
		// The same typo through either channel exits the same way; these two
		// used to differ (103 with a usage dump vs 71 with one line).
		{Name: "value typo on the command line", Args: []string{"-m", "abc", "--assert-ok", url("/ok")}, Want: exitBadInvocation, Diag: "invalid argument"},
		{Name: "value typo in the environment", Env: map[string]string{"HTTP_ASSERT_MAX_TIME": "abc"}, Args: []string{"--assert-ok", url("/ok")}, Want: exitBadInvocation, Diag: "Invalid value for HTTP_ASSERT_MAX_TIME"},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			r := run(t, tc.Env, tc.Args...)
			assertExit(t, r, tc.Want)
			if tc.Diag != "" {
				assertContains(t, r, tc.Diag)
			}
		})
	}
}

// TestE2ERequestOptions covers the request-shaping options against the echo
// endpoint. The failing assertion is deliberate: it makes the CLI dump the
// response, which is the only way the echoed request becomes observable.
func TestE2ERequestOptions(t *testing.T) {
	echo := url("/echo")
	dump := []string{"--assert-body-eq", "never-matches"}

	t.Run("method", func(t *testing.T) {
		r := run(t, nil, append(append([]string{"-X", "PUT"}, dump...), echo)...)
		assertContains(t, r, `\"method\":\"PUT\"`)
	})

	t.Run("repeated headers accumulate", func(t *testing.T) {
		r := run(t, nil, append(append([]string{"-H", "X-One: 1", "-H", "X-Two: 2"}, dump...), echo)...)
		assertContains(t, r, "X-One")
		assertContains(t, r, "X-Two")
	})

	t.Run("header names are canonicalised", func(t *testing.T) {
		r := run(t, nil, append(append([]string{"-H", "x-lower-case: v"}, dump...), echo)...)
		assertContains(t, r, "X-Lower-Case")
	})

	t.Run("body is sent", func(t *testing.T) {
		r := run(t, nil, append(append([]string{"-X", "POST", "-d", "payload-here"}, dump...), echo)...)
		assertContains(t, r, "payload-here")
	})

	t.Run("multi-value response headers match on any value", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-header-eq", "Set-Cookie: b=2", url("/multi")), exitOK)
	})
}

// TestE2EDataImpliesPost pins that -d implies POST and curl's default
// Content-Type, with -X and an explicit Content-Type header overriding.
//
// This lived in the known-issues file asserting that the method stayed GET;
// it flipped when #28 was fixed.
func TestE2EDataImpliesPost(t *testing.T) {
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

// TestE2EHostMapping covers the option that distinguishes this tool from curl.
func TestE2EHostMapping(t *testing.T) {
	target := "http://mapped.invalid/ok"

	t.Run("host and port", func(t *testing.T) {
		assertExit(t, run(t, nil, "--maphost", "mapped.invalid:80="+hostPort(), "--assert-ok", target), exitOK)
	})

	t.Run("port wildcard", func(t *testing.T) {
		assertExit(t, run(t, nil, "--maphost", "*:80="+hostPort(), "--assert-ok", target), exitOK)
	})

	t.Run("repeated mappings accumulate", func(t *testing.T) {
		assertExit(t, run(t, nil,
			"--maphost", "decoy.invalid:80=127.0.0.1:9",
			"--maphost", "mapped.invalid:80="+hostPort(),
			"--assert-ok", target), exitOK)
	})

	t.Run("unmapped hosts are untouched", func(t *testing.T) {
		// The unmapped .invalid host fails DNS, so this is a transport
		// failure: proof the mapping was not applied.
		r := run(t, nil, "--maphost", "other.invalid:80="+hostPort(), "--assert-ok", target)
		assertExit(t, r, exitTransportFail)
	})

	t.Run("mapping is logged at debug level", func(t *testing.T) {
		r := run(t, nil, "-v", "--maphost", "mapped.invalid:80="+hostPort(), "--assert-ok", target)
		assertContains(t, r, "mapped.invalid:80")
	})
}

// TestE2ELogLevels covers every accepted --log-level value. info and debug are
// here for completeness: without them the level parser is only half exercised,
// and a typo in an unused branch would go unnoticed.
func TestE2ELogLevels(t *testing.T) {
	okURL := url("/ok")

	t.Run("debug shows the host-mapping summary", func(t *testing.T) {
		r := run(t, nil, "--log-level", "debug",
			"--maphost", "mapped.invalid:80="+hostPort(),
			"--assert-ok", "http://mapped.invalid/ok")
		assertExit(t, r, exitOK)
		assertContains(t, r, "HostMappings")
	})

	t.Run("info is the default and reports the result", func(t *testing.T) {
		r := run(t, nil, "--log-level", "info", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertContains(t, r, "PASSED")
	})

	t.Run("info matches the unset default", func(t *testing.T) {
		explicit := run(t, nil, "--log-level", "info", "--assert-ok", okURL)
		implicit := run(t, nil, "--assert-ok", okURL)
		if (explicit.Output() == "") != (implicit.Output() == "") {
			t.Fatalf("explicit info differs from the default\n%q\n%q", explicit.Output(), implicit.Output())
		}
	})

	t.Run("warn and error suppress the result line", func(t *testing.T) {
		for _, lvl := range []string{"warn", "error"} {
			r := run(t, nil, "--log-level", lvl, "--assert-ok", okURL)
			assertExit(t, r, exitOK)
			assertNotContains(t, r, "PASSED")
		}
	})
}

// TestE2EVerbosityPriority pins how the three verbosity options resolve when
// more than one asks for a level: the command line beats the environment as a
// whole, within one channel -s beats -v beats --log-level, and every request
// overridden to a different level is announced rather than silently dropped.
//
// Previously the ties resolved by an undocumented ladder with no announcement,
// so -v -s ran verbose with no sign that -s lost (#29). A conflict is never an
// error: the run proceeds at the winning level.
func TestE2EVerbosityPriority(t *testing.T) {
	okURL := url("/ok")

	t.Run("-s beats -v, and says so despite winning silence", func(t *testing.T) {
		r := run(t, nil, "-v", "-s", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertNotContains(t, r, "PASSED")
		assertContains(t, r, "Warning: -s overrides -v")
	})

	t.Run("-s beats --log-level", func(t *testing.T) {
		r := run(t, nil, "-s", "--log-level", "debug", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertNotContains(t, r, "PASSED")
		assertContains(t, r, "Warning: -s overrides --log-level debug")
	})

	t.Run("-v beats --log-level", func(t *testing.T) {
		r := run(t, nil, "-v", "--log-level", "error", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertContains(t, r, "PASSED")
		assertContains(t, r, "Warning: -v overrides --log-level error")
	})

	t.Run("the command line beats the environment's silent", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_SILENT": "true"}, "-v", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertContains(t, r, "PASSED")
		assertContains(t, r, "Warning: -v overrides HTTP_ASSERT_SILENT")
	})

	// The case that used to invert the documented precedence: the
	// environment's log level ate an explicit -v.
	t.Run("the command line beats the environment's log level", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_LOG_LEVEL": "error"}, "-v", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertContains(t, r, "PASSED")
		assertContains(t, r, "Warning: -v overrides HTTP_ASSERT_LOG_LEVEL")
	})

	t.Run("within the environment, silent still beats verbose", func(t *testing.T) {
		r := run(t, map[string]string{
			"HTTP_ASSERT_VERBOSE": "true",
			"HTTP_ASSERT_SILENT":  "true",
		}, "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertNotContains(t, r, "PASSED")
		assertContains(t, r, "Warning: HTTP_ASSERT_SILENT overrides HTTP_ASSERT_VERBOSE")
	})

	// Nothing was overridden to a different level, so there is nothing to
	// announce.
	t.Run("an agreeing pair warns about nothing", func(t *testing.T) {
		r := run(t, nil, "-v", "--log-level", "debug", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertContains(t, r, "PASSED")
		assertNotContains(t, r, "Warning:")
	})

	// A false boolean declines to ask for a level rather than asking for the
	// default: no conflict with -s, and it cancels the environment's verbose
	// instead of losing to it.
	t.Run("-v=false neither conflicts nor resurrects", func(t *testing.T) {
		r := run(t, nil, "-v=false", "-s", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertNotContains(t, r, "PASSED")
		assertNotContains(t, r, "Warning:")

		r = run(t, map[string]string{"HTTP_ASSERT_VERBOSE": "true"}, "-v=false", "--assert-ok", okURL)
		assertExit(t, r, exitOK)
		assertContains(t, r, "PASSED") // info, not debug: the env verbose is gone
		assertNotContains(t, r, "Warning:")
	})
}

// TestE2EHeaderPresenceAssertions covers the bare-name form of both header
// assertion flags, where the absence of a value means "assert present" rather
// than "assert equal to the empty string".
func TestE2EHeaderPresenceAssertions(t *testing.T) {
	for _, flag := range []string{"--assert-header", "--assert-header-eq"} {
		t.Run(flag, func(t *testing.T) {
			t.Run("present", func(t *testing.T) {
				assertExit(t, run(t, nil, flag, "X-Api-Version", url("/ok")), exitOK)
			})

			t.Run("absent", func(t *testing.T) {
				r := run(t, nil, flag, "X-Api-Version", url("/created"))
				assertExit(t, r, exitAssertFail)
				assertContains(t, r, "expected to be present")
			})
		})
	}
}

// TestE2ELargePayloadCropped covers the crop path in the response dump: bodies
// over 256 bytes are truncated and the hidden byte count is reported.
func TestE2ELargePayloadCropped(t *testing.T) {
	r := run(t, nil, "--assert-body-eq", "never-matches", url("/big"))
	assertExit(t, r, exitAssertFail)
	assertContains(t, r, "Payload is cropped")
	assertContains(t, r, "4744 bytes are hidden")
}

// TestE2EAssertEmptyBody covers the expectations an empty body satisfies.
//
// They were unreachable until #22: both --assert-body-eq and --assert-body
// checked whether the body was empty before checking what was asked of it, so
// a 204 could not be asserted to have the body a 204 is defined to have.
func TestE2EAssertEmptyBody(t *testing.T) {
	t.Run("--assert-body-eq '' passes against a 204", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body-eq", "", url("/empty")), exitOK)
	})

	t.Run("--assert-body '^$' passes against a 204", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body", "^$", url("/empty")), exitOK)
	})

	// The inverse still fails, so the fix did not simply stop checking.
	t.Run("--assert-body-eq '' fails against a body", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", "", url("/ok"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `body: expected "", got`)
	})

	// And the wording that the old guard existed to produce is still there.
	t.Run("an empty body still reports as missing", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", "value", url("/empty"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `body: expected "value", missing`)
	})
}

// TestE2EAssertBooleanNegation pins what =false means on the two boolean
// assertions. It used to mean two different things: --assert-ok=false selected
// the inverse assertion, while --assert-body-empty=false selected none at all
// and killed the run with "no assertions defined" (#32).
func TestE2EAssertBooleanNegation(t *testing.T) {
	for _, tc := range []struct {
		Name string
		Args []string
		Want int
	}{
		// --assert-ok, both directions, both outcomes.
		{"--assert-ok on a 200", []string{"--assert-ok", url("/ok")}, exitOK},
		{"--assert-ok on a 500", []string{"--assert-ok", url("/500")}, exitAssertFail},
		{"--assert-ok=false on a 500", []string{"--assert-ok=false", url("/500")}, exitOK},
		{"--assert-ok=false on a 200", []string{"--assert-ok=false", url("/ok")}, exitAssertFail},

		// --assert-body-empty, the same four.
		{"--assert-body-empty on a 204", []string{"--assert-body-empty", url("/empty")}, exitOK},
		{"--assert-body-empty on a body", []string{"--assert-body-empty", url("/ok")}, exitAssertFail},
		{"--assert-body-empty=false on a body", []string{"--assert-body-empty=false", url("/ok")}, exitOK},
		{"--assert-body-empty=false on a 204", []string{"--assert-body-empty=false", url("/empty")}, exitAssertFail},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assertExit(t, run(t, nil, tc.Args...), tc.Want)
		})
	}

	// The failure mode that made this a bug rather than an inconsistency: a
	// user names an assertion and is told there are none.
	t.Run("a negated flag is never treated as no assertion", func(t *testing.T) {
		for _, f := range []string{"--assert-ok=false", "--assert-body-empty=false"} {
			r := run(t, nil, f, url("/empty"))
			assertNotContains(t, r, "no assertions defined")
		}
	})

	// Negation still counts as naming the flag, so repeating it is refused
	// exactly as repeating the positive form is.
	t.Run("a repeated negation is still refused", func(t *testing.T) {
		r := run(t, nil, "--assert-ok", "--assert-ok=false", url("/ok"))
		assertExit(t, r, exitBadInvocation)
		assertContains(t, r, "was given 2 times")
	})
}

// TestE2EHeaderRequiresASeparator covers the -H values the CLI refuses.
//
// A name on its own used to be sent as a header with an empty value. curl reads
// the same input as "remove this header", so a user reaching for that idiom got
// the opposite of what they asked for, silently (#33).
func TestE2EHeaderRequiresASeparator(t *testing.T) {
	for _, tc := range []struct {
		Name string
		Arg  string
		Diag string
	}{
		{
			Name: "a bare name",
			Arg:  "BareHeader",
			Diag: `Invalid value for --header flag: "BareHeader" has no ':' separator`,
		},
		{
			// The common typo, and the reason this is worth an error rather
			// than a best guess.
			Name: "a name with a value but no colon",
			Arg:  "X-Api-Key abc123",
			Diag: "has no ':' separator",
		},
		{
			// A colon is present but there is nothing in front of it, which
			// would put a nameless header on the wire.
			Name: "a colon with no name",
			Arg:  ": value",
			Diag: `has no header name before the ':'`,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := run(t, nil, "-H", tc.Arg, "--assert-ok", url("/ok"))
			assertExit(t, r, exitBadInvocation)
			assertContains(t, r, tc.Diag)
		})
	}

	// An empty -H is refused too, but only alongside another one. pflag reads a
	// string array back through its own string form, in which a lone empty
	// value serialises to "[]" and parses back as no values at all -- so a
	// solitary -H '' never reaches this program. Harmless (it asks for no
	// header and gets none) and worth pinning, because the pair below proves
	// the validation itself is not what lets the single case through.
	t.Run("an empty value alongside another", func(t *testing.T) {
		r := run(t, nil, "-H", "X-Ok: 1", "-H", "", "--assert-ok", url("/ok"))
		assertExit(t, r, exitBadInvocation)
		assertContains(t, r, "has no ':' separator")
	})

	t.Run("a solitary empty value is dropped before it arrives", func(t *testing.T) {
		assertExit(t, run(t, nil, "-H", "", "--assert-ok", url("/ok")), exitOK)
	})

	// The message names the fix, because "no separator" alone leaves the
	// reader guessing whether an empty value is even expressible.
	t.Run("the error says how to send an empty value", func(t *testing.T) {
		r := run(t, nil, "-H", "X-Foo", "--assert-ok", url("/ok"))
		assertContains(t, r, `write "X-Foo:" to send the header with an empty value`)
	})

	t.Run("and that spelling works", func(t *testing.T) {
		r := run(t, nil, "-H", "X-Foo:", "--assert-body", `"X-Foo":\[""\]`, url("/echo"))
		assertExit(t, r, exitOK)
	})

	// Ordinary headers are untouched.
	t.Run("a normal header still works", func(t *testing.T) {
		r := run(t, nil, "-H", "X-Probe: 1", "--assert-body", `"X-Probe":\["1"\]`, url("/echo"))
		assertExit(t, r, exitOK)
	})

	// The parser is shared with the header assertions, where a bare name is
	// meaningful. Validating inside it would have broken these.
	t.Run("--assert-header* still accept a bare name", func(t *testing.T) {
		for _, f := range []string{"--assert-header", "--assert-header-eq"} {
			r := run(t, nil, f, "Content-Type", url("/ok"))
			assertExit(t, r, exitOK)
		}
		assertExit(t, run(t, nil, "--assert-header-missing", "X-Absent", url("/ok")), exitOK)
	})
}

// TestE2EBadPatternIsRejected pins that an unparseable pattern is reported
// as an invalid flag value rather than reaching the runtime as a panic.
//
// This lived in the known-issues file asserting the panic; it flipped when
// #17 was fixed.
func TestE2EBadPatternIsRejected(t *testing.T) {
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

			assertExit(t, r, exitBadInvocation)
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

// TestE2EHeaderMultiValue pins the semantics of a repeated response header and
// the way its values are rendered when an assertion fails (#97).
//
// A header can carry several values, which is a different thing from repeating
// the flag. The matching rule was already the behaviour; it was never written
// down anywhere a user would look, and the failure was the only hint -- in Go's
// own slice syntax.
func TestE2EHeaderMultiValue(t *testing.T) {
	t.Run("any value satisfies --assert-header-eq", func(t *testing.T) {
		for _, v := range []string{"a=1", "b=2"} {
			assertExit(t, run(t, nil, "--assert-header-eq", "Set-Cookie: "+v, url("/multi")), exitOK)
		}
	})

	t.Run("any value satisfies --assert-header", func(t *testing.T) {
		for _, p := range []string{`^a=\d$`, `^b=\d$`} {
			assertExit(t, run(t, nil, "--assert-header", "Set-Cookie: "+p, url("/multi")), exitOK)
		}
	})

	// The strict one: a header carrying any value at all is not missing.
	t.Run("--assert-header-missing fails on a multi-valued header", func(t *testing.T) {
		r := run(t, nil, "--assert-header-missing", "Set-Cookie", url("/multi"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `header[Set-Cookie]: expected to be missing, got "a=1", "b=2"`)
	})

	t.Run("a failure lists the values a person would write", func(t *testing.T) {
		r := run(t, nil, "--assert-header-eq", "Set-Cookie: nope", url("/multi"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `header[Set-Cookie]: expected "nope", got "a=1", "b=2"`)
	})

	// One value reads as one value. `got ["v1"]` said something bracketed had
	// happened to a header that only ever had one value.
	t.Run("a single-valued header is not rendered as a list", func(t *testing.T) {
		r := run(t, nil, "--assert-header-eq", "X-Api-Version: nope", url("/ok"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `header[X-Api-Version]: expected "nope", got "v1"`)
	})

	t.Run("--assert-header renders the same way", func(t *testing.T) {
		r := run(t, nil, "--assert-header", `X-Api-Version: ^v9$`, url("/ok"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `header[X-Api-Version]: expected to match "^v9$", got "v1"`)
	})

	// The regression guard: no assertion may leak Go's slice syntax again.
	t.Run("no failure leaks Go slice syntax", func(t *testing.T) {
		for _, args := range [][]string{
			{"--assert-header-eq", "Set-Cookie: nope", url("/multi")},
			{"--assert-header", "Set-Cookie: ^nope$", url("/multi")},
			{"--assert-header-missing", "Set-Cookie", url("/multi")},
			{"--assert-header-eq", "X-Api-Version: nope", url("/ok")},
		} {
			r := run(t, nil, args...)
			assertNotContains(t, r, `got [`)
		}
	})
}
