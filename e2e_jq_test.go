package main_test

import "testing"

// --assert-jq is the answer to asserting on JSON with a regex, which breaks on
// whitespace, key order and escaping. jq yields the verdict itself, so there is
// no path-and-value syntax to invent and no question of whether 5 means the
// number or the string.

func TestE2EAssertJQ(t *testing.T) {
	t.Run("a query that holds", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-jq", `.status == "success"`, url("/json")), exitOK)
	})

	t.Run("a query that does not", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `.status == "nope"`, url("/json"))
		assertExit(t, r, exitAssertFail)
		// The failure names the query, because a run can carry several.
		assertContains(t, r, `jq[.status == "nope"]: expected true, got false`)
	})

	// What regex over JSON cannot do, and the reason this exists.
	t.Run("it reads structure, not text", func(t *testing.T) {
		for _, q := range []string{
			`.users | length == 2`,
			`.meta.version == "v1"`,
			`.count == 2`,
			`.active`,
			`[.users[].name] == ["alice","bob"]`,
			`.users | map(select(.active)) | length == 2`,
			`.status | test("^suc")`,
		} {
			t.Run(q, func(t *testing.T) {
				assertExit(t, run(t, nil, "--assert-jq", q, url("/json")), exitOK)
			})
		}
	})
}

// TestE2EAssertJQAccumulates: repeating the flag adds assertions rather than
// replacing them, as it does for the three header assertions. --assert-jq is a
// string array, so rejectRepeats leaves it alone.
func TestE2EAssertJQAccumulates(t *testing.T) {
	t.Run("every query is checked", func(t *testing.T) {
		assertExit(t, run(t, nil,
			"--assert-jq", `.status == "success"`,
			"--assert-jq", `.count == 2`,
			"--assert-jq", `.users | length == 2`,
			url("/json")), exitOK)
	})

	// The failure mode worth pinning: an earlier query passing must not carry
	// a later one that fails.
	t.Run("a later query still fails the run", func(t *testing.T) {
		r := run(t, nil,
			"--assert-jq", `.status == "success"`,
			"--assert-jq", `.count == 99`,
			url("/json"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `jq[.count == 99]`)
	})

	t.Run("and an earlier one does too", func(t *testing.T) {
		r := run(t, nil,
			"--assert-jq", `.count == 99`,
			"--assert-jq", `.status == "success"`,
			url("/json"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `jq[.count == 99]`)
	})

	// Every failure is reported, not just the first -- the property that
	// separates this tool from `curl && jq`.
	t.Run("several failures are all reported", func(t *testing.T) {
		r := run(t, nil,
			"--assert-jq", `.count == 98`,
			"--assert-jq", `.count == 99`,
			url("/json"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "2 assertions failed:")
		assertContains(t, r, `jq[.count == 98]`)
		assertContains(t, r, `jq[.count == 99]`)
	})
}

// TestE2EAssertJQComposes: the other body assertions keep working alongside it,
// and all of them accumulate into the same run.
func TestE2EAssertJQComposes(t *testing.T) {
	t.Run("with every other kind of assertion", func(t *testing.T) {
		assertExit(t, run(t, nil,
			"--assert-jq", `.status == "success"`,
			"--assert-jq", `.users | length == 2`,
			"--assert-body", `"alice"`,
			"--assert-body-empty=false",
			"--assert-status", "200",
			"--assert-ok",
			"--assert-header-eq", "Content-Type: application/json",
			url("/json")), exitOK)
	})

	// Mixed failures aggregate across kinds, not just within one kind.
	t.Run("failures aggregate across kinds", func(t *testing.T) {
		r := run(t, nil,
			"--assert-jq", `.status == "nope"`,
			"--assert-body", "never-matches",
			"--assert-status", "999",
			url("/json"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "3 assertions failed:")
		assertContains(t, r, "jq[")
		assertContains(t, r, "body: expected to match")
		assertContains(t, r, "status: expected 999")
	})

	// --assert-body-eq is exact-match over the raw text and must be unaffected
	// by anything jq does to the decoded document.
	t.Run("--assert-body-eq still sees the raw text", func(t *testing.T) {
		assertExit(t, run(t, nil,
			"--assert-jq", `.count == 2`,
			"--assert-body-eq", `{"status":"success","count":2}`,
			url("/json-gzip")), exitOK)
	})

	// jq reads the decoded payload, like every other body assertion (#27).
	t.Run("it runs against a decompressed body", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-jq", `.status == "success"`,
			url("/json-gzip")), exitOK)
	})
}

// TestE2EAssertJQRejectsBadQueries: a broken expression is a broken command
// line, so it is refused before the request is made rather than reported as a
// failed assertion afterwards.
func TestE2EAssertJQRejectsBadQueries(t *testing.T) {
	for _, tc := range []struct {
		Name  string
		Query string
		Diag  string
	}{
		{"unterminated", `.users[`, "unexpected EOF"},
		{"a dangling pipe", `.a |`, "unexpected EOF"},
		// Parses cleanly and fails only on compile, which is why both stages
		// run at flag time.
		{"an undefined function", `nope(.)`, "function not defined: nope/1"},
		{"an undefined variable", `. as $x | $y`, "variable not defined: $y"},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := run(t, nil, "--assert-jq", tc.Query, url("/json"))
			assertExit(t, r, exitBadInvocation)
			assertContains(t, r, "Invalid value for --assert-jq flag")
			assertContains(t, r, tc.Diag)
		})
	}

	// Refused before the request, so a bad query cannot be mistaken for a
	// service that answered wrongly.
	t.Run("the request is never made", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `.users[`, url("/json"))
		assertNotContains(t, r, "[:] HTTP")
	})
}

// TestE2EAssertJQBodyProblems covers the responses a query cannot be run
// against at all. Each says the body is at fault rather than the expression.
func TestE2EAssertJQBodyProblems(t *testing.T) {
	t.Run("a body that is not JSON", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `. == 1`, url("/created"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "body: expected JSON")
	})

	t.Run("an empty body", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `. == 1`, url("/empty"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "body: expected JSON")
	})

	// An encoding nothing can decode reports the encoding, not invalid JSON.
	t.Run("a body still encoded", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `. == 1`, url("/unsupported"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "body: response is compress-encoded")
	})

	// A query yielding nothing has checked nothing, so it must not pass.
	t.Run("a query that matches nothing", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `.users[] | select(.id == 99) | .active`, url("/json"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, "expected true, got no output")
	})

	// A non-boolean result is a mis-written assertion, and the failure shows
	// what the query actually produced so the reader can see why.
	t.Run("a query that yields a value rather than a verdict", func(t *testing.T) {
		r := run(t, nil, "--assert-jq", `.status`, url("/json"))
		assertExit(t, r, exitAssertFail)
		assertContains(t, r, `expected true, got "success"`)
	})
}
