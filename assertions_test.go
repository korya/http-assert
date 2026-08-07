package main

import (
	"fmt"
	"net/http"
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

			checkErr(t, "ok", ok(res), wantOK)
			checkErr(t, "nok", nok(res), wantNOK)
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
				checkErr(t, fmt.Sprintf("expected %d", expected), assertions[expected](res), want)
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
	match := AssertHeaderMatch("taRget", `(?i)^val.*$`)
	for _, tc := range testCases {
		t.Run(tc.CaseName, func(t *testing.T) {
			res := &httpResponse{
				Response: &http.Response{
					Header: http.Header(tc.Header),
				},
			}

			if tc.ExpMissing {
				checkErr(t, "present", present(res), `header[taRgEt]: expected to be present, missing`)
				checkErr(t, "missing", missing(res), "")
			} else {
				checkErr(t, "present", present(res), "")
				// The values are echoed back, so match rather than pin them.
				checkErrMatch(t, "missing", missing(res),
					`header\[taRgEt\]: expected to be missing, got \[.*\]$`)
			}

			checkErr(t, "equal", equal(res), tc.ExpEqualError)
			checkErr(t, "match", match(res), tc.ExpMatchError)
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
	match := AssertBodyMatch(`(?i)^val.*$`)
	for _, tc := range testCases {
		t.Run(tc.CaseName, func(t *testing.T) {
			res := &httpResponse{BodyBytes: tc.Body}

			checkErr(t, "empty", empty(res), tc.ExpEmptyError)
			checkErr(t, "equal", equal(res), tc.ExpEqualError)
			checkErr(t, "match", match(res), tc.ExpMatchError)
		})
	}
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
	match := AssertRedirectMatch(`(?i)example\.[a-z]*/$`)
	for _, tc := range testCases {
		t.Run(tc.CaseName, func(t *testing.T) {
			res := &httpResponse{
				Response: &http.Response{
					StatusCode: tc.StatusCode,
					Status:     strings.Join(strings.Split(strconv.Itoa(tc.StatusCode), ""), "_"),
					Header:     http.Header(tc.Header),
				},
			}

			checkErr(t, "equal", equal(res), tc.ExpEqualError)
			checkErr(t, "match", match(res), tc.ExpMatchError)
		})
	}
}
