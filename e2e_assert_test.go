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
				assertExit(t, r, exitRequestFail)
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

	assertExit(t, r, exitRequestFail)
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
		{Name: "assertion failed", Args: []string{"--assert-ok", url("/500")}, Want: exitRequestFail, Diag: "assertions failed"},
		{Name: "connection refused", Args: []string{"--assert-ok", "http://127.0.0.1:9/"}, Want: exitRequestFail, Diag: "failed to send request"},
		{Name: "dns failure", Args: []string{"--assert-ok", "http://nonexistent.invalid/"}, Want: exitRequestFail, Diag: "failed to send request"},
		{Name: "unsupported scheme", Args: []string{"--assert-ok", "ftp://example.com/x"}, Want: exitRequestFail, Diag: "unsupported protocol scheme"},
		{Name: "tls verification", Args: []string{"--assert-ok", tlsSrv.URL}, Want: exitRequestFail, Diag: "certificate"},
		{Name: "invalid log level", Args: []string{"--log-level", "trace", "--assert-ok", url("/ok")}, Want: exitBadFlagVal, Diag: "Invalid value for --log-level"},
		{Name: "invalid maphost", Args: []string{"--maphost", "garbage", "--assert-ok", url("/ok")}, Want: exitBadFlagVal, Diag: "Invalid value for --maphost"},
		{Name: "invalid method", Args: []string{"-X", "BAD METHOD", "--assert-ok", url("/ok")}, Want: exitBadRequest, Diag: "Cannot create request"},
		{Name: "malformed url", Args: []string{"--assert-ok", "ht!tp://[bad"}, Want: exitBadRequest, Diag: "Cannot create request"},
		{Name: "no url", Args: []string{"--assert-ok"}, Want: exitUsage, Diag: "accepts 1 arg(s)"},
		{Name: "too many urls", Args: []string{"--assert-ok", url("/ok"), url("/created")}, Want: exitUsage, Diag: "accepts 1 arg(s)"},
		{Name: "unknown flag", Args: []string{"--nope", url("/ok")}, Want: exitUsage, Diag: "unknown flag"},
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
		r := run(t, nil, "--maphost", "other.invalid:80="+hostPort(), "--assert-ok", target)
		assertExit(t, r, exitRequestFail)
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
				assertExit(t, r, exitRequestFail)
				assertContains(t, r, "expected to be present")
			})
		})
	}
}

// TestE2ELargePayloadCropped covers the crop path in the response dump: bodies
// over 256 bytes are truncated and the hidden byte count is reported.
func TestE2ELargePayloadCropped(t *testing.T) {
	r := run(t, nil, "--assert-body-eq", "never-matches", url("/big"))
	assertExit(t, r, exitRequestFail)
	assertContains(t, r, "Payload is cropped")
	assertContains(t, r, "4744 bytes are hidden")
}
