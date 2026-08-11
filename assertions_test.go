package main

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func Test_AssertStatusOK(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		StatusCode int
		Status     string
		OK         bool
	}{
		{0, "UNKNOWN", false},
		{100, "CONTINUE", false},
		{200, "OK", true},
		{201, "Created", true},
		{204, "No Content", true},
		{299, "Custom OK Response", true},
		{300, "Multiple Choice", true},
		{301, "Moved Permanently", true},
		{307, "Temporary Redirect", true},
		{399, "Custom Redirect", true},
		{400, "Bad Request", false},
		{401, "Unauthorized", false},
		{429, "Too Many Requests", false},
		{500, "Internal Server Error", false},
		{914, "Custom Response", false},
	}

	ok := AssertStatusOK()
	nok := AssertStatusNOK()
	for _, tc := range testCases {
		t.Run(strconv.Itoa(tc.StatusCode), func(t *testing.T) {
			res := &httpResponse{
				Response: &http.Response{
					StatusCode: tc.StatusCode,
					Status:     tc.Status,
				},
			}

			// Exactly one of the two must hold, so each case checks both.
			wantOK, wantNOK := "", fmt.Sprintf("nok: expected NOK, got %d (%q)", tc.StatusCode, tc.Status)
			if !tc.OK {
				wantOK, wantNOK = fmt.Sprintf("ok: expected OK, got %d (%q)", tc.StatusCode, tc.Status), ""
			}

			checkErr(t, "ok", check(ok, res), wantOK)
			checkErr(t, "nok", check(nok, res), wantNOK)
		})
	}
}

func Test_AssertStatusEqual(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		StatusCode int
		Status     string
		OK         bool
	}{
		{0, "UNKNOWN", false},
		{100, "CONTINUE", false},
		{200, "OK", true},
		{201, "Created", true},
		{204, "No Content", true},
		{299, "Custom OK Response", true},
		{300, "Multiple Choice", false},
		{301, "Moved Permanently", false},
		{307, "Temporary Redirect", false},
		{400, "Bad Request", false},
		{429, "Too Many Requests", false},
		{500, "Internal Server Error", false},
		{914, "Custom Response", false},
	}

	// 1 is never a real status, so that assertion must always fail; 200 and 429
	// appear in the table and must pass for their own case only.
	assertions := map[int]Assertion{1: AssertStatusEqual(1), 200: AssertStatusEqual(200), 429: AssertStatusEqual(429)}
	for _, tc := range testCases {
		t.Run(strconv.Itoa(tc.StatusCode), func(t *testing.T) {
			res := &httpResponse{
				Response: &http.Response{
					StatusCode: tc.StatusCode,
					Status:     tc.Status,
				},
			}

			for _, expected := range []int{1, 200, 429} {
				want := ""
				if tc.StatusCode != expected {
					want = fmt.Sprintf("status: expected %d, got %d (%q)", expected, tc.StatusCode, tc.Status)
				}
				checkErr(t, fmt.Sprintf("expected %d", expected), check(assertions[expected], res), want)
			}
		})
	}
}

func Test_AssertHeader(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		CaseName      string
		Header        map[string][]string
		ExpMissing    bool
		ExpEqualError string
		ExpMatchError string
	}{
		{
			CaseName:      "No headers",
			ExpMissing:    true,
			ExpEqualError: `header[taRgET]: expected "value", missing`,
			ExpMatchError: `header[taRget]: expected to match "(?i)^val.*$", missing`,
		},
		{
			CaseName: "Missing",
			Header: map[string][]string{
				"one": []string{"value"},
				"two": []string{"value", "v", "2"},
			},
			ExpMissing:    true,
			ExpEqualError: `header[taRgET]: expected "value", missing`,
			ExpMatchError: `header[taRget]: expected to match "(?i)^val.*$", missing`,
		},
		{
			CaseName: "Present",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{""},
				"two":    []string{"value", "v", "2"},
			},
			ExpEqualError: `header[taRgET]: expected "value", got [""]`,
			ExpMatchError: `header[taRget]: expected to match "(?i)^val.*$", got [""]`,
		},
		{
			CaseName: "Non-empty but non-matching value",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{"v"},
				"two":    []string{"value", "v", "2"},
			},
			ExpEqualError: `header[taRgET]: expected "value", got ["v"]`,
			ExpMatchError: `header[taRget]: expected to match "(?i)^val.*$", got ["v"]`,
		},
		{
			CaseName: "Matching value",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{"vAl"},
				"two":    []string{"value", "v", "2"},
			},
			ExpEqualError: `header[taRgET]: expected "value", got ["vAl"]`,
		},
		{
			CaseName: "Exact value",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{"value"},
				"two":    []string{"value", "v", "2"},
			},
		},
		// Multiple values
		{
			CaseName: "Multiple: no matching value",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{"one", "two", "three"},
				"two":    []string{"value", "v", "2"},
			},
			ExpEqualError: `header[taRgET]: expected "value", got ["one" "two" "three"]`,
			ExpMatchError: `header[taRget]: expected to match "(?i)^val.*$", got ["one" "two" "three"]`,
		},
		{
			CaseName: "Multple: Matching value",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{"one", "two", "vAl", "three"},
				"two":    []string{"value", "v", "2"},
			},
			ExpEqualError: `header[taRgET]: expected "value", got ["one" "two" "vAl" "three"]`,
		},
		{
			CaseName: "Multple: Exact value",
			Header: map[string][]string{
				"one":    []string{"value"},
				"Target": []string{"one", "vAl", "two", "value"},
				"two":    []string{"value", "v", "2"},
			},
		},
	}

	present := AssertHeaderPresent("taRgEt")
	missing := AssertHeaderMissing("taRgEt")
	equal := AssertHeaderEqual("taRgET", "value")
	match, err := AssertHeaderMatch("taRget", `(?i)^val.*$`)
	if err != nil {
		t.Fatalf("unexpected error building the assertion: %s", err)
	}
	for _, tc := range testCases {
		t.Run(tc.CaseName, func(t *testing.T) {
			res := &httpResponse{
				Response: &http.Response{
					Header: http.Header(tc.Header),
				},
			}

			if tc.ExpMissing {
				checkErr(t, "present", check(present, res), `header[taRgEt]: expected to be present, missing`)
				checkErr(t, "missing", check(missing, res), "")
			} else {
				checkErr(t, "present", check(present, res), "")
				// The values are echoed back, so match rather than pin them.
				checkErrMatch(t, "missing", check(missing, res),
					`header\[taRgEt\]: expected to be missing, got \[.*\]$`)
			}

			checkErr(t, "equal", check(equal, res), tc.ExpEqualError)
			checkErr(t, "match", check(match, res), tc.ExpMatchError)
		})
	}
}

func Test_AssertBody(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		CaseName      string
		Body          []byte
		ExpEmptyError string
		ExpEqualError string
		ExpMatchError string
	}{
		{
			CaseName:      "Null body",
			ExpEqualError: `body: expected "value", missing`,
			ExpMatchError: `body: expected to match "(?i)^val.*$", missing`,
		},
		{
			CaseName:      "Empty body",
			Body:          []byte{},
			ExpEqualError: `body: expected "value", missing`,
			ExpMatchError: `body: expected to match "(?i)^val.*$", missing`,
		},
		{
			CaseName:      "Non-matching body",
			Body:          []byte("v"),
			ExpEmptyError: `body: expected to be empty, got "v"`,
			ExpEqualError: `body: expected "value", got "v"`,
			ExpMatchError: `body: expected to match "(?i)^val.*$", got "v"`,
		},
		{
			CaseName:      "Matching body",
			Body:          []byte("vAl"),
			ExpEmptyError: `body: expected to be empty, got "vAl"`,
			ExpEqualError: `body: expected "value", got "vAl"`,
		},
		{
			CaseName:      "Exact body",
			Body:          []byte("value"),
			ExpEmptyError: `body: expected to be empty, got "value"`,
		},
	}

	empty := AssertBodyEmpty()
	equal := AssertBodyEqual("value")
	match, err := AssertBodyMatch(`(?i)^val.*$`)
	if err != nil {
		t.Fatalf("unexpected error building the assertion: %s", err)
	}
	for _, tc := range testCases {
		t.Run(tc.CaseName, func(t *testing.T) {
			res := &httpResponse{BodyBytes: tc.Body}

			checkErr(t, "empty", check(empty, res), tc.ExpEmptyError)
			checkErr(t, "equal", check(equal, res), tc.ExpEqualError)
			checkErr(t, "match", check(match, res), tc.ExpMatchError)
		})
	}
}

// Test_AssertBody_emptyIsAssertable covers the expectations that an empty body
// satisfies. They were unreachable while emptiness was checked before the
// comparison: the guard existed to word the failure nicely and ended up
// deciding it (#22).
func Test_AssertBody_emptyIsAssertable(t *testing.T) {
	t.Parallel()

	// Patterns an empty body legitimately matches. `.*` is the one a user is
	// most likely to reach for, `^$` the one they mean.
	patterns := []string{"^$", ".*", `\A\z`, ""}

	t.Run("equal to the empty string", func(t *testing.T) {
		res := &httpResponse{BodyBytes: []byte{}}
		checkErr(t, "equal", check(AssertBodyEqual(""), res), "")

		// And a nil body, which is what a 204 produces.
		checkErr(t, "equal, nil body", check(AssertBodyEqual(""), &httpResponse{}), "")
	})

	for _, p := range patterns {
		t.Run("matching "+strconv.Quote(p), func(t *testing.T) {
			a, err := AssertBodyMatch(p)
			if err != nil {
				t.Fatalf("cannot build the assertion: %s", err)
			}

			checkErr(t, "match", check(a, &httpResponse{BodyBytes: []byte{}}), "")
			checkErr(t, "match, nil body", check(a, &httpResponse{}), "")
		})
	}

	// The verdict moved; the wording did not. A body that is empty when
	// something was expected still reads as "missing" rather than `got ""`.
	t.Run("an empty body still reports as missing", func(t *testing.T) {
		res := &httpResponse{BodyBytes: []byte{}}
		checkErr(t, "equal", check(AssertBodyEqual("value"), res), `body: expected "value", missing`)

		a, err := AssertBodyMatch("^value$")
		if err != nil {
			t.Fatalf("cannot build the assertion: %s", err)
		}
		checkErr(t, "match", check(a, res), `body: expected to match "^value$", missing`)
	})

	// The inverse must keep failing: a non-empty body is not the empty string.
	t.Run("a non-empty body does not equal the empty string", func(t *testing.T) {
		res := &httpResponse{BodyBytes: []byte("x")}
		checkErr(t, "equal", check(AssertBodyEqual(""), res), `body: expected "", got "x"`)
	})
}

func Test_AssertRedirect(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		CaseName      string
		StatusCode    int
		Header        map[string][]string
		ExpEqualError string
		ExpMatchError string
	}{
		{
			CaseName:      "No status, no headers",
			ExpEqualError: `redirect: wrong HTTP status: got 0 ("0")`,
			ExpMatchError: `redirect: wrong HTTP status: got 0 ("0")`,
		},
		{
			CaseName:      "OK status, no headers",
			StatusCode:    200,
			ExpEqualError: `redirect: wrong HTTP status: got 200 ("2_0_0")`,
			ExpMatchError: `redirect: wrong HTTP status: got 200 ("2_0_0")`,
		},
		{
			CaseName:      "Error status, no headers",
			StatusCode:    400,
			ExpEqualError: `redirect: wrong HTTP status: got 400 ("4_0_0")`,
			ExpMatchError: `redirect: wrong HTTP status: got 400 ("4_0_0")`,
		},
		{
			CaseName:      "3xx, no headers",
			StatusCode:    300,
			ExpEqualError: `redirect: no Location header`,
			ExpMatchError: `redirect: no Location header`,
		},
		{
			CaseName:   "3xx, Location missing",
			StatusCode: 301,
			Header: map[string][]string{
				"one": []string{"example.com/"},
				"two": []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: no Location header`,
			ExpMatchError: `redirect: no Location header`,
		},
		{
			CaseName:   "3xx, Location empty",
			StatusCode: 302,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{""},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got ""`,
			ExpMatchError: `redirect: wrong Location: expected to match "(?i)example\\.[a-z]*/$", got ""`,
		},
		{
			CaseName:   "3xx, Location mismatch",
			StatusCode: 303,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"exa"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got "exa"`,
			ExpMatchError: `redirect: wrong Location: expected to match "(?i)example\\.[a-z]*/$", got "exa"`,
		},

		{
			CaseName:   "3xx, Location match",
			StatusCode: 304,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"eXaMpLe.Com/"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got "eXaMpLe.Com/"`,
		},
		{
			CaseName:   "3xx, Location equal",
			StatusCode: 305,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"https://example.com/"},
				"two":      []string{"example.com/", "v", "2"},
			},
		},
		{
			CaseName:   "Wrong status, Location equal",
			StatusCode: 204,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"https://example.com/"},
				"two":      []string{"example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong HTTP status: got 204 ("2_0_4")`,
			ExpMatchError: `redirect: wrong HTTP status: got 204 ("2_0_4")`,
		},
		// Multiple values
		{
			CaseName:   "Multiple: 3xx, Location mismatch",
			StatusCode: 306,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"one", "two", "three"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got "one"`,
			ExpMatchError: `redirect: wrong Location: expected to match "(?i)example\\.[a-z]*/$", got "one"`,
		},
		{
			CaseName:   "Multiple: 3xx, Location first match",
			StatusCode: 307,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"eXaMpLe.Com/", "two", "vAl", "three"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got "eXaMpLe.Com/"`,
		},
		{
			CaseName:   "Multiple: 3xx, Location second match",
			StatusCode: 307,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"one", "eXaMpLe.Com/", "vAl", "three"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got "one"`,
			ExpMatchError: `redirect: wrong Location: expected to match "(?i)example\\.[a-z]*/$", got "one"`,
		},
		{
			CaseName:   "Multiple: 3xx, Location first equal",
			StatusCode: 308,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"https://example.com/", "vAl", "two", "example.com/"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
		},
		{
			CaseName:   "Multiple: 3xx, Location second equal",
			StatusCode: 308,
			Header: map[string][]string{
				"one":      []string{"example.com/"},
				"Location": []string{"one", "https://example.com/", "two", "example.com/"},
				"two":      []string{"https://example.com/", "v", "2"},
			},
			ExpEqualError: `redirect: wrong Location: expected "https://example.com/", got "one"`,
			ExpMatchError: `redirect: wrong Location: expected to match "(?i)example\\.[a-z]*/$", got "one"`,
		},
	}

	equal := AssertRedirectEqual(`https://example.com/`)
	match, err := AssertRedirectMatch(`(?i)example\.[a-z]*/$`)
	if err != nil {
		t.Fatalf("unexpected error building the assertion: %s", err)
	}
	for _, tc := range testCases {
		t.Run(tc.CaseName, func(t *testing.T) {
			res := &httpResponse{
				Response: &http.Response{
					StatusCode: tc.StatusCode,
					Status:     strings.Join(strings.Split(strconv.Itoa(tc.StatusCode), ""), "_"),
					Header:     http.Header(tc.Header),
				},
			}

			checkErr(t, "equal", check(equal, res), tc.ExpEqualError)
			checkErr(t, "match", check(match, res), tc.ExpMatchError)
		})
	}
}

// Test_AssertBodyNotEmpty is the assertion --assert-body-empty=false selects.
// It had no constructor at all, which is why that flag registered nothing (#32).
func Test_AssertBodyNotEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name string
		Body []byte
		Want string
	}{
		{Name: "a body satisfies it", Body: []byte("x")},
		{
			Name: "an empty body does not",
			Body: []byte{},
			Want: "body: expected to be non-empty, got nothing",
		},
		{
			// What a 204 produces. It must fail the same way an empty slice
			// does, not differently.
			Name: "a nil body does not",
			Want: "body: expected to be non-empty, got nothing",
		},
		{
			// Whitespace is content. The assertion is about presence, not
			// meaning, and trimming here would make it about both.
			Name: "whitespace counts as a body",
			Body: []byte(" "),
		},
	}

	a := AssertBodyNotEmpty()
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			checkErr(t, "not-empty", check(a, &httpResponse{BodyBytes: tc.Body}), tc.Want)
		})
	}

	// The pair must be exact opposites on every input, or =false means
	// something subtly other than "not that".
	t.Run("it is the exact inverse of AssertBodyEmpty", func(t *testing.T) {
		empty := AssertBodyEmpty()
		for _, body := range [][]byte{nil, {}, []byte(" "), []byte("x"), []byte("longer body")} {
			res := &httpResponse{BodyBytes: body}
			if (check(empty, res) == nil) == (check(a, res) == nil) {
				t.Errorf("both agree on %q; they must disagree", string(body))
			}
		}
	})
}

// Test_AssertMatchConstructorsRejectBadPatterns covers the failure path the
// pattern-based constructors gained when they stopped panicking (#17).
func Test_AssertMatchConstructorsRejectBadPatterns(t *testing.T) {
	t.Parallel()

	constructors := map[string]func(string) (Assertion, error){
		"AssertBodyMatch":     AssertBodyMatch,
		"AssertRedirectMatch": AssertRedirectMatch,
		"AssertHeaderMatch":   func(p string) (Assertion, error) { return AssertHeaderMatch("X-Any", p) },
	}

	for name, build := range constructors {
		t.Run(name, func(t *testing.T) {
			t.Run("unparseable pattern", func(t *testing.T) {
				a, err := build("[unclosed")
				if err == nil {
					t.Fatal("expected an error for an unparseable pattern, got nil")
				}
				if a != nil {
					t.Error("expected a nil Assertion alongside the error")
				}
				// The parser's own reason is passed through, not swallowed.
				if !strings.Contains(err.Error(), "missing closing ]") {
					t.Errorf("error = %q, want it to explain the syntax problem", err)
				}
			})

			t.Run("valid pattern", func(t *testing.T) {
				a, err := build(`^ok$`)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if a == nil {
					t.Fatal("expected an Assertion")
				}
			})
		})
	}
}

// Test_AssertionIdentity pins the structure assertions gained when Assertion
// became an interface (#56): every assertion names its kind, and a failure
// carries the parts of its sentence as data rather than only the sentence.
//
// These are the fields #45 serializes, so a change here is a change to the
// machine-readable contract, not an internal detail. The Message is covered by
// the tables above and deliberately not repeated.
func Test_AssertionIdentity(t *testing.T) {
	t.Parallel()

	statusRes := func(code int, status string) *httpResponse {
		return &httpResponse{
			Response: &http.Response{StatusCode: code, Status: status},
		}
	}
	headerRes := func(h http.Header) *httpResponse {
		return &httpResponse{
			Response: &http.Response{StatusCode: 200, Status: "200 OK", Header: h},
		}
	}

	jq, err := AssertJQ(".n == 1")
	if err != nil {
		t.Fatalf("cannot build the jq assertion: %s", err)
	}

	tests := []struct {
		Name      string
		Assertion Assertion
		Res       *httpResponse
		Kind      string
		Target    string
		Expected  any
		Actual    any
	}{
		{
			Name: "ok", Assertion: AssertStatusOK(),
			Res:  statusRes(500, "500 Internal Server Error"),
			Kind: "ok", Expected: "2xx-3xx", Actual: 500,
		},
		{
			Name: "nok", Assertion: AssertStatusNOK(),
			Res:  statusRes(200, "200 OK"),
			Kind: "nok", Expected: "not 2xx-3xx", Actual: 200,
		},
		{
			Name: "status", Assertion: AssertStatusEqual(200),
			Res:  statusRes(500, "500 Internal Server Error"),
			Kind: "status", Expected: 200, Actual: 500,
		},
		{
			Name: "header present", Assertion: AssertHeaderPresent("X-Absent"),
			Res:  headerRes(http.Header{}),
			Kind: "header", Target: "X-Absent", Expected: "present",
		},
		{
			Name: "header equal", Assertion: AssertHeaderEqual("X-A", "want"),
			Res:  headerRes(http.Header{"X-A": []string{"got"}}),
			Kind: "header", Target: "X-A", Expected: "want", Actual: []string{"got"},
		},
		{
			Name: "body equal", Assertion: AssertBodyEqual("want"),
			Res:  &httpResponse{BodyBytes: []byte("got")},
			Kind: "body", Expected: "want", Actual: "got",
		},
		{
			Name: "redirect", Assertion: AssertRedirectEqual("/there"),
			Res: headerRes(http.Header{"Location": []string{"/elsewhere"}}),
			// A 200 never reaches the Location comparison, so this is the
			// precondition failure, which reports the status it wanted.
			Kind: "redirect", Expected: "3xx", Actual: 200,
		},
		{
			Name: "jq", Assertion: jq, Res: jqResponse(`{"n":2}`),
			Kind: "jq", Target: ".n == 1", Expected: true, Actual: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			if got := tc.Assertion.Kind(); got != tc.Kind {
				t.Errorf("Kind() = %q, want %q", got, tc.Kind)
			}

			f, err := tc.Assertion.Check(tc.Res)
			if err != nil {
				t.Fatalf("unexpected evaluation error: %s", err)
			}
			if f == nil {
				t.Fatal("expected a Failure, got none")
			}

			// Kind is stamped by Check rather than written by each
			// constructor, so the two can never drift apart.
			if f.Kind != tc.Kind {
				t.Errorf("Failure.Kind = %q, want %q", f.Kind, tc.Kind)
			}
			if f.Target != tc.Target {
				t.Errorf("Target = %q, want %q", f.Target, tc.Target)
			}
			if !reflect.DeepEqual(f.Expected, tc.Expected) {
				t.Errorf("Expected = %#v, want %#v", f.Expected, tc.Expected)
			}
			if !reflect.DeepEqual(f.Actual, tc.Actual) {
				t.Errorf("Actual = %#v, want %#v", f.Actual, tc.Actual)
			}
			if f.Message == "" {
				t.Error("Message is empty; the human path reads this")
			}
		})
	}
}

// Test_AssertionCheckSeparatesFailureFromError covers the distinction the
// interface exists to draw: a response that was read and disagreed is a
// Failure, and one that could not be evaluated at all is an error. Both still
// fail the run -- doOnce collects them into one list -- but only one of them
// has an Expected and an Actual to report.
func Test_AssertionCheckSeparatesFailureFromError(t *testing.T) {
	t.Parallel()

	t.Run("an assertion that holds reports neither", func(t *testing.T) {
		f, err := AssertBodyEqual("same").Check(&httpResponse{BodyBytes: []byte("same")})
		if f != nil || err != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", f, err)
		}
	})

	t.Run("an undecodable body is an error, not a Failure", func(t *testing.T) {
		res := &httpResponse{
			Encoding:  "compress",
			DecodeErr: errors.New(`no decoder for "compress"`),
		}

		for name, a := range map[string]Assertion{
			"body equal": AssertBodyEqual("x"),
			"body empty": AssertBodyEmpty(),
		} {
			t.Run(name, func(t *testing.T) {
				f, err := a.Check(res)
				if f != nil {
					t.Errorf("got a Failure %+v; an unevaluable assertion has no Expected/Actual", f)
				}
				if err == nil {
					t.Fatal("expected an evaluation error, got nil")
				}
				if !strings.Contains(err.Error(), "was not decoded") {
					t.Errorf("error = %q, want it to name the encoding problem", err)
				}
			})
		}
	})

	t.Run("a status assertion is unaffected by an undecodable body", func(t *testing.T) {
		res := &httpResponse{
			Response:  &http.Response{StatusCode: 200, Status: "200 OK"},
			Encoding:  "compress",
			DecodeErr: errors.New(`no decoder for "compress"`),
		}

		f, err := AssertStatusOK().Check(res)
		if f != nil || err != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", f, err)
		}
	})
}
