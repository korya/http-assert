package main_test

import (
	"strings"
	"testing"
)

// --location is the only option that changes which response the assertions run
// against, so what it does is worth pinning precisely: the default it departs
// from, the boundary of the hop limit it introduces, and the two combinations
// it refuses.
//
// Every test here stays on the loopback test server. /redirect points at a real
// external host and must never appear with -L (see e2e_server_test.go).

// TestE2ERedirectDefaultIsNotToFollow pins the behaviour --location opts out
// of. Without it a 3xx is the response, which is what --assert-redirect* reads.
func TestE2ERedirectDefaultIsNotToFollow(t *testing.T) {
	t.Run("the 3xx is the response", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-status", "302", url("/hop?n=1")), exitOK)
	})

	t.Run("the destination is never fetched", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", "arrived", url("/hop?n=1"))
		assertExit(t, r, exitRequestFail)
	})
}

func TestE2ERedirectFollowing(t *testing.T) {
	t.Run("-L reaches the destination", func(t *testing.T) {
		assertExit(t, run(t, nil, "-L", "--assert-body-eq", "arrived", url("/hop?n=3")), exitOK)
	})

	// The point of the feature: --assert-status and --assert-body describe the
	// far end of the chain, not the 3xx that started it.
	t.Run("every assertion applies to the final response", func(t *testing.T) {
		r := run(t, nil, "-L",
			"--assert-status", "200",
			"--assert-body", `"status":"success"`,
			"--assert-header-eq", "X-Api-Version: v1",
			url("/redirect-local"))
		assertExit(t, r, exitOK)
	})

	// The hops are the one part of the run that leaves no trace in the final
	// response, so they are logged.
	t.Run("each hop is logged", func(t *testing.T) {
		r := run(t, nil, "-L", "--assert-ok", url("/hop?n=2"))
		assertExit(t, r, exitOK)
		assertContains(t, r, "[>] 1 GET")
		assertContains(t, r, "[>] 2 GET")
	})

	// A 302 rewrites POST to GET and drops the body; 308 does not. The body can
	// only be replayed because http.NewRequest sets GetBody for a strings.Reader.
	t.Run("308 replays the method and body", func(t *testing.T) {
		// One pattern rather than two flags: the echo must show both the
		// method and the body, and a passing run prints neither.
		r := run(t, nil, "-L", "-X", "POST", "-d", "replayed-payload",
			"--assert-body", `"body":"replayed-payload".*"method":"POST"`,
			url("/redirect-308"))
		assertExit(t, r, exitOK)
	})

	// Redirects are resolved before the transport dials, so a mapping covers
	// every hop rather than only the first.
	t.Run("--maphost applies to every hop", func(t *testing.T) {
		r := run(t, nil, "--maphost", "mapped.invalid:80="+hostPort(),
			"-L", "--assert-body-eq", "arrived", "http://mapped.invalid/hop?n=2")
		assertExit(t, r, exitOK)
	})
}

// TestE2ERedirectHopLimit covers the boundary. net/http's own default policy is
// off by one against its message ("stopped after 10 redirects" follows nine),
// so --max-redirs N following exactly N hops is worth asserting from both sides.
func TestE2ERedirectHopLimit(t *testing.T) {
	t.Run("exactly N hops is allowed", func(t *testing.T) {
		assertExit(t, run(t, nil, "-L", "--max-redirs", "3",
			"--assert-body-eq", "arrived", url("/hop?n=3")), exitOK)
	})

	t.Run("N+1 hops is refused", func(t *testing.T) {
		r := run(t, nil, "-L", "--max-redirs", "2", "--assert-ok", url("/hop?n=3"))
		assertExit(t, r, exitRequestFail)
		// Reported as this program stopping the chain, not as the network
		// failing -- the reader would otherwise go looking for a broken host.
		assertContains(t, r, "redirect chain was not followed to the end")
		assertContains(t, r, "--max-redirs is 2")
		assertNotContains(t, r, "failed to send request")
	})

	// curl's meaning for zero, and the reason the comparison is > rather than
	// the >= net/http uses: zero has to refuse the first hop, not permit it.
	t.Run("zero refuses every redirect", func(t *testing.T) {
		r := run(t, nil, "-L", "--max-redirs", "0", "--assert-ok", url("/hop?n=1"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "redirect chain was not followed to the end")
	})

	t.Run("the default of 10 applies when unset", func(t *testing.T) {
		assertExit(t, run(t, nil, "-L", "--assert-body-eq", "arrived", url("/hop?n=10")), exitOK)

		r := run(t, nil, "-L", "--assert-ok", url("/hop?n=11"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "--max-redirs is 10")
	})

	// Nothing else terminates this one.
	t.Run("a redirect loop ends at the limit", func(t *testing.T) {
		r := run(t, nil, "-L", "--assert-ok", url("/redirect-loop"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "redirect chain was not followed to the end")
	})
}

// TestE2ERedirectRejectedCombinations covers the requests the CLI refuses to
// serve rather than resolve silently. Each would otherwise produce a green run
// that checked something other than what was asked for.
func TestE2ERedirectRejectedCombinations(t *testing.T) {
	for _, tc := range []struct {
		Name string
		Args []string
		Diag string
	}{
		{
			Name: "-L with --assert-redirect",
			Args: []string{"-L", "--assert-redirect", ".*", url("/ok")},
			Diag: "Flags --location and --assert-redirect cannot be used together",
		},
		{
			Name: "-L with --assert-redirect-eq",
			Args: []string{"-L", "--assert-redirect-eq", "/ok", url("/ok")},
			Diag: "Flags --location and --assert-redirect-eq cannot be used together",
		},
		{
			Name: "--max-redirs without -L",
			Args: []string{"--max-redirs", "5", "--assert-ok", url("/ok")},
			Diag: "bounds a redirect chain that is not being followed",
		},
		{
			Name: "a negative --max-redirs",
			Args: []string{"-L", "--max-redirs=-1", "--assert-ok", url("/ok")},
			Diag: "Invalid value for --max-redirs flag: -1",
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := run(t, nil, tc.Args...)
			assertExit(t, r, exitBadFlagVal)
			assertContains(t, r, tc.Diag)
		})
	}

	// The two assertions keep working on their own; the exclusion is about the
	// combination, not about deprecating them.
	t.Run("--assert-redirect alone is unaffected", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-redirect", "/ok", url("/redirect-local")), exitOK)
	})
}

// TestE2ERedirectFailureDumpNamesTheDestination: after a chain the dumped
// request is the one that started it, and the response came from somewhere
// else. Without the extra line the two read as a single exchange.
func TestE2ERedirectFailureDumpNamesTheDestination(t *testing.T) {
	r := run(t, nil, "-L", "--assert-status", "999", url("/redirect-local"))
	assertExit(t, r, exitRequestFail)

	assertContains(t, r, "FAILED: GET "+url("/redirect-local"))
	assertContains(t, r, "Followed to: GET "+url("/ok"))

	// And it stays out of the way when nothing was followed.
	plain := run(t, nil, "--assert-status", "999", url("/ok"))
	if strings.Contains(plain.Output(), "Followed to:") {
		t.Fatalf("the redirect line appears without a redirect\n%s", plain.Output())
	}
}
