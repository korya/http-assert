package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/itchyny/gojq"
)

const jqDoc = `{"status":"success","count":5,"active":true,"nothing":null,
 "users":[{"id":1,"name":"alice","active":true},{"id":2,"name":"bob","active":false}]}`

// jqResponse builds a response carrying the document above, or whatever body a
// case needs.
func jqResponse(body string) *httpResponse {
	r := response("200 OK", http.Header{}, "")
	r.BodyBytes = []byte(body)

	return &r
}

// Test_AssertJQ is the verdict table: what a query must yield for the assertion
// to hold, and what the failure says when it does not.
func Test_AssertJQ(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name  string
		Query string
		Body  string
		// Want is the expected error text, empty for a passing assertion.
		Want string
	}{
		{Name: "a true comparison", Query: `.status == "success"`, Body: jqDoc},
		{Name: "a numeric comparison", Query: `.count == 5`, Body: jqDoc},
		{Name: "a boolean field read directly", Query: `.active`, Body: jqDoc},
		{Name: "a builtin", Query: `.users | length == 2`, Body: jqDoc},
		{Name: "a regexp, which jq has of its own", Query: `.status | test("^suc")`, Body: jqDoc},
		{
			// Several outputs, all true. Every one has to hold, or the
			// assertion would pass on the strength of the first.
			Name:  "every output true",
			Query: `.users[] | .id > 0`,
			Body:  jqDoc,
		},
		{
			Name:  "one output false among several",
			Query: `.users[] | .active`,
			Body:  jqDoc,
			Want:  `jq[.users[] | .active]: expected true, got false`,
		},
		{
			// The case that makes this more than a nicety: a query matching
			// nothing has checked nothing, and a pass would be a green run
			// with no check behind it.
			Name:  "no output at all",
			Query: `.users[] | select(.id == 99) | .active`,
			Body:  jqDoc,
			Want:  `jq[.users[] | select(.id == 99) | .active]: expected true, got no output`,
		},
		{
			Name:  "a non-boolean output",
			Query: `.status`,
			Body:  jqDoc,
			Want:  `jq[.status]: expected true, got "success"`,
		},
		{
			// A missing key yields null rather than an error, so without this
			// the failure would read as an unhelpful "got <nil>".
			Name:  "a missing key",
			Query: `.nope`,
			Body:  jqDoc,
			Want:  `jq[.nope]: expected true, got null`,
		},
		{
			Name:  "a JSON null field",
			Query: `.nothing`,
			Body:  jqDoc,
			Want:  `jq[.nothing]: expected true, got null`,
		},
		{
			// Reported as a broken query, not as a failed assertion. The
			// service answered correctly; the expression is what is wrong.
			Name:  "a runtime type error",
			Query: `.status + 1`,
			Body:  jqDoc,
			Want:  `jq[.status + 1]: cannot add: string ("success") and number (1)`,
		},
		{
			Name:  "an object output",
			Query: `.users[0]`,
			Body:  jqDoc,
			Want:  `jq[.users[0]]: expected true, got {"active":true,"id":1,"name":"alice"}`,
		},
		{
			Name:  "a body that is not JSON",
			Query: `. == 1`,
			Body:  "plain text",
			Want:  "body: expected JSON, got invalid character 'p' looking for beginning of value",
		},
		{
			// A 204 has no body to decode. The message names the body rather
			// than blaming the expression.
			Name:  "an empty body",
			Query: `. == 1`,
			Body:  "",
			Want:  "body: expected JSON, got unexpected end of JSON input",
		},
		{
			// Valid JSON that is not an object. jq is happy; so is this.
			Name:  "a top-level array",
			Query: `length == 3`,
			Body:  `[1,2,3]`,
		},
		{
			Name:  "a top-level scalar",
			Query: `. == 42`,
			Body:  `42`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			a, err := AssertJQ(tc.Query)
			if err != nil {
				t.Fatalf("cannot build the assertion: %s", err)
			}

			checkErr(t, "jq", check(a, jqResponse(tc.Body)), tc.Want)
		})
	}
}

// Test_AssertJQ_rejectsBadQueries covers the two ways a query can be invalid.
// They are caught at different stages, and only catching one would let a typo
// reach the response and be reported as a failed assertion.
func Test_AssertJQ_rejectsBadQueries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name  string
		Query string
		Want  string
		Why   string
	}{
		{"unterminated", `.users[`, "unexpected EOF", "rejected while parsing"},
		{"a dangling pipe", `.a |`, "unexpected EOF", "rejected while parsing"},
		{
			// Parses cleanly; only compiling notices. This is why both stages
			// run before the request is made.
			Name: "an undefined function", Query: `nope(.)`,
			Want: "function not defined: nope/1", Why: "rejected while compiling",
		},
		{
			Name: "an undefined variable", Query: `. as $x | $y`,
			Want: "variable not defined: $y", Why: "rejected while compiling",
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			a, err := AssertJQ(tc.Query)
			if err == nil {
				t.Fatalf("%q was accepted; expected it to be %s", tc.Query, tc.Why)
			}
			if a != nil {
				t.Error("expected a nil Assertion alongside the error")
			}
			if got := err.Error(); !strings.Contains(got, tc.Want) {
				t.Errorf("error = %q, want it to contain %q", got, tc.Want)
			}
		})
	}
}

// Test_AssertJQ_boundsARunawayQuery: jq is a real language, so a query can
// simply never finish. Nothing else a caller can type makes this program hang
// -- the request is bounded by --max-time, the retry loop by --retry, and an
// --assert-body pattern cannot blow up because Go's regexp engine is
// linear-time. This is what keeps that true.
//
// Driven through runJQ with a short deadline rather than through the assertion
// with the real one: waiting out 10s twice would add 20s to a unit suite that
// otherwise runs in a quarter of a second, on every one of CI's six legs.
func Test_AssertJQ_boundsARunawayQuery(t *testing.T) {
	t.Parallel()

	const short = 50 * time.Millisecond

	// Both compile cleanly. They are valid jq rather than malformed input, so
	// no amount of validation at flag time could catch them -- which is why the
	// bound has to exist at all.
	for _, query := range []string{`def f: f; f`, `reduce range(1e9) as $i (0; .+$i)`} {
		t.Run(query, func(t *testing.T) {
			q, err := gojq.Parse(query)
			if err != nil {
				t.Fatalf("expected %q to parse: %s", query, err)
			}
			code, err := gojq.Compile(q)
			if err != nil {
				t.Fatalf("expected %q to compile: %s", query, err)
			}

			done := make(chan error, 1)
			start := time.Now()
			go func() {
				// Either return means the query did not succeed; the test
				// only cares that it stopped, and why is asserted below.
				f, err := runJQ(code, query, jqResponse(jqDoc), short)
				if err == nil && f != nil {
					err = f
				}
				done <- err
			}()

			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a non-terminating query reported success")
				}
				// Stopping early would mean it never really ran, so the
				// deadline is asserted from both sides.
				if d := time.Since(start); d < short {
					t.Errorf("stopped after %v, before the %v deadline: %s", d, short, err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("still running long past the deadline")
			}
		})
	}
}

// Test_jqTimeout pins the deadline the assertion actually uses. The test above
// injects its own, so without this the real value could drift to something
// useless and nothing would notice.
func Test_jqTimeout(t *testing.T) {
	t.Parallel()

	if got, want := jqTimeout, 10*time.Second; got != want {
		t.Errorf("jqTimeout = %v, want %v", got, want)
	}
}

// Test_decodeJSON_parsesOnce pins the sharing. Ten --assert-jq flags read one
// response, and it should be decoded once rather than ten times.
func Test_decodeJSON_parsesOnce(t *testing.T) {
	t.Parallel()

	res := jqResponse(jqDoc)

	first, err := res.decodeJSON()
	if err != nil {
		t.Fatalf("first decode: %s", err)
	}
	second, err := res.decodeJSON()
	if err != nil {
		t.Fatalf("second decode: %s", err)
	}

	// Same map, not an equal one: a second Unmarshal would produce a distinct
	// value, so identity is what proves the body was parsed once.
	m1, ok1 := first.(map[string]any)
	m2, ok2 := second.(map[string]any)
	if !ok1 || !ok2 {
		t.Fatalf("expected a JSON object, got %T and %T", first, second)
	}
	m1["probe"] = true
	if _, carried := m2["probe"]; !carried {
		t.Error("decodeJSON re-parsed the body instead of sharing the first result")
	}

	t.Run("a failure is remembered too", func(t *testing.T) {
		bad := jqResponse("not json")
		if _, err := bad.decodeJSON(); err == nil {
			t.Fatal("expected an error")
		}
		if _, err := bad.decodeJSON(); err == nil {
			t.Error("the second call lost the error the first recorded")
		}
	})

	// A body that is still compressed must report that, not invalid JSON --
	// the reader would otherwise go looking for a malformed payload (#27).
	t.Run("an undecoded body reports the encoding", func(t *testing.T) {
		enc := jqResponse(jqDoc)
		enc.Encoding = "compress"
		enc.DecodeErr = errors.New("no decoder for \"compress\"")

		_, err := enc.decodeJSON()
		checkErrMatch(t, "decodeJSON", err, `^body: response is compress-encoded`)
	})
}
