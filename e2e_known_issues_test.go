package main_test

import (
	"strings"
	"testing"
)

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

// TestKnownIssue18PayloadTruncated: the response dump replaces the body with a
// rendered form but leaves Content-Length untouched, so http.Response.Write
// truncates whatever it renders to the original byte count.
func TestKnownIssue18PayloadTruncated(t *testing.T) {
	t.Run("silent mode truncates the placeholder", func(t *testing.T) {
		characterizes(t, 18, "'<< Payload is omitted >>' is cut to the body's length")

		// The body is "boom" (4 bytes), so only 4 bytes of the placeholder survive.
		r := run(t, nil, "-s", "--assert-ok", url("/500"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "  <<")
		assertNotContains(t, r, "Payload is omitted >>")
	})

	t.Run("binary body truncates the hex dump", func(t *testing.T) {
		characterizes(t, 18, "an 8-byte binary body renders as a single hex offset")

		r := run(t, nil, "--assert-body-eq", "never-matches", url("/binary"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "00000000")
		// The real dump would carry the bytes and an ASCII gutter.
		assertNotContains(t, r, "|")
	})
}

// TestKnownIssue19RequestBodyMissing: the request is dumped after the transport
// has drained its body, so Content-Length advertises bytes that are not shown.
func TestKnownIssue19RequestBodyMissing(t *testing.T) {
	characterizes(t, 19, "the request dump omits the -d payload it advertises")

	r := run(t, nil, "-X", "POST", "-d", "sent-but-not-shown", "--assert-status", "999", url("/echo"))
	assertExit(t, r, exitRequestFail)

	// The echoed response proves the body reached the server...
	assertContains(t, r, "sent-but-not-shown")
	// ...while the request dump advertises its length and shows nothing.
	assertContains(t, r, "Content-Length: 18")

	reqDump, _, _ := strings.Cut(r.Output(), "HTTP/1.1 200 OK")
	if strings.Contains(reqDump, "sent-but-not-shown") {
		t.Fatal("request dump now includes the body; #19 is fixed -- update this test")
	}
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

// TestKnownIssue22EmptyBodyEqualsNeverPasses: --assert-body-eq "" short-circuits
// on an empty body and reports that the empty string is "missing".
func TestKnownIssue22EmptyBodyEqualsNeverPasses(t *testing.T) {
	characterizes(t, 22, `--assert-body-eq "" cannot pass, even against a 204`)

	r := run(t, nil, "--assert-body-eq", "", url("/empty"))
	assertExit(t, r, exitRequestFail)
	assertContains(t, r, `body: expected "", missing`)
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

// TestKnownIssue27GzipBreaksBodyAssertions: setting Accept-Encoding by hand
// disables Go's transparent decompression, so assertions see compressed bytes.
func TestKnownIssue27GzipBreaksBodyAssertions(t *testing.T) {
	t.Run("transparent decompression by default", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body", `"status":"success"`, url("/gzip")), exitOK)
	})

	t.Run("caller-set Accept-Encoding breaks it", func(t *testing.T) {
		characterizes(t, 27, "body assertions run against gzip bytes, with no warning")

		r := run(t, nil, "-H", "Accept-Encoding: gzip", "--assert-body", `"status":"success"`, url("/gzip"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "Content-Encoding: gzip")
	})
}

// TestKnownIssue28DataDoesNotImplyPost: curl switches to POST for -d; this does not.
func TestKnownIssue28DataDoesNotImplyPost(t *testing.T) {
	characterizes(t, 28, "-d keeps the GET method its help text says it changes")

	r := run(t, nil, "-d", "payload", "--assert-body-eq", "never-matches", url("/echo"))
	assertContains(t, r, `\"method\":\"GET\"`)
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

// TestKnownIssue32NegatedBooleanFlagsDiffer: --assert-ok=false registers the
// inverse assertion; --assert-body-empty=false registers nothing.
func TestKnownIssue32NegatedBooleanFlagsDiffer(t *testing.T) {
	t.Run("assert-ok=false asserts NOT ok", func(t *testing.T) {
		characterizes(t, 32, "an undocumented negation that happens to be useful")
		assertExit(t, run(t, nil, "--assert-ok=false", url("/500")), exitOK)
	})

	t.Run("assert-body-empty=false registers nothing", func(t *testing.T) {
		characterizes(t, 32, "the same syntax on a sibling flag is a no-op")
		r := run(t, nil, "--assert-body-empty=false", url("/ok"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "no assertions defined")
	})
}

// TestKnownIssue33BareHeaderSendsEmptyValue: a -H value with no colon parses to
// an empty value and is sent as an empty-valued header.
//
// curl treats `-H 'X-Foo'` as "remove this header" and requires `-H 'X-Foo;'` to
// send an empty one, so the divergence is real -- but the header is NOT dropped,
// which is what #33 originally claimed. The issue text has been corrected.
func TestKnownIssue33BareHeaderSendsEmptyValue(t *testing.T) {
	characterizes(t, 33, "-H without a colon sends an empty-valued header rather than being rejected")

	r := run(t, nil, "-H", "BareHeader", "--assert-body-eq", "never-matches", url("/echo"))
	assertExit(t, r, exitRequestFail)

	// Canonicalised to Bareheader, present, with an empty value.
	assertContains(t, r, "Bareheader")
	assertContains(t, r, `\"Bareheader\":[\"\"]`)
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
