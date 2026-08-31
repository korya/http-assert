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

// TestKnownIssue23WildcardMaphostUnreachable: hostMapping.Matches handles "*"
// and "*:*", but the parser rejects both, so the branches are dead code.
func TestKnownIssue23WildcardMaphostUnreachable(t *testing.T) {
	for _, src := range []string{"*", "*:*"} {
		t.Run(src, func(t *testing.T) {
			characterizes(t, 23, "the parser rejects a wildcard the matcher supports")

			r := run(t, nil, "--maphost", src+"="+hostPort(), "--assert-ok", "http://mapped.invalid/ok")
			assertExit(t, r, exitBadInvocation)
			assertContains(t, r, "Invalid value for --maphost flag")
		})
	}
}

// TestKnownIssue26MaphostErrorDiscarded: parseHostMappings produces a precise
// message that mustParseHostMappings throws away.
func TestKnownIssue26MaphostErrorDiscarded(t *testing.T) {
	characterizes(t, 26, "the specific parse error is replaced by the raw flag slice")

	r := run(t, nil, "--maphost", "garbage", "--assert-ok", url("/ok"))
	assertExit(t, r, exitBadInvocation)
	assertContains(t, r, "Invalid value for --maphost flag: [garbage]")
	assertNotContains(t, r, "has no separator")
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
