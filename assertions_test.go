package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/onsi/gomega"
)

func Test_AssertStatusOK(t *testing.T) {
	g := gomega.NewWithT(t)

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

	ok := AssertStatusOK()
	nok := AssertStatusNOK()
	for _, tc := range testCases {
		res := &httpResponse{
			Response: &http.Response{
				StatusCode: tc.StatusCode,
				Status:     tc.Status,
			},
		}

		msg := strconv.Itoa(tc.StatusCode)
		if tc.OK {
			g.Expect(ok(res)).To(gomega.BeNil(), msg)
			g.Expect(nok(res)).To(gomega.MatchError(
				fmt.Sprintf("nok: expected NOK, got %d (%q)", tc.StatusCode, tc.Status),
			), msg)
		} else {
			g.Expect(ok(res)).To(gomega.MatchError(
				fmt.Sprintf("ok: expected OK, got %d (%q)", tc.StatusCode, tc.Status),
			), msg)
			g.Expect(nok(res)).To(gomega.BeNil(), msg)
		}
	}
}

func Test_AssertStatusEqual(t *testing.T) {
	g := gomega.NewWithT(t)

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

	a1 := AssertStatusEqual(1)
	a200 := AssertStatusEqual(200)
	a429 := AssertStatusEqual(429)
	for _, tc := range testCases {
		res := &httpResponse{
			Response: &http.Response{
				StatusCode: tc.StatusCode,
				Status:     tc.Status,
			},
		}

		msg := strconv.Itoa(tc.StatusCode)
		g.Expect(a1(res)).To(gomega.MatchError(
			fmt.Sprintf("status: expected 1, got %d (%q)", tc.StatusCode, tc.Status),
		))
		if tc.StatusCode != 200 {
			g.Expect(a200(res)).To(gomega.MatchError(
				fmt.Sprintf("status: expected 200, got %d (%q)", tc.StatusCode, tc.Status),
			))
		} else {
			g.Expect(a200(res)).To(gomega.BeNil(), msg)
		}
		if tc.StatusCode != 429 {
			g.Expect(a429(res)).To(gomega.MatchError(
				fmt.Sprintf("status: expected 429, got %d (%q)", tc.StatusCode, tc.Status),
			))
		} else {
			g.Expect(a429(res)).To(gomega.BeNil(), msg)
		}
	}
}

func Test_AssertHeader(t *testing.T) {
	g := gomega.NewWithT(t)

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
	equal := AssertHeaderEqual("taRgET", "value")
	match := AssertHeaderMatch("taRget", `(?i)^val.*$`)
	for _, tc := range testCases {
		res := &httpResponse{
			Response: &http.Response{
				Header: http.Header(tc.Header),
			},
		}

		if !tc.ExpMissing {
			g.Expect(present(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(present(res)).To(gomega.MatchError(
				`header[taRgEt]: expected to be present, missing`,
			), tc.CaseName)
		}

		if tc.ExpEqualError == "" {
			g.Expect(equal(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(equal(res)).To(gomega.MatchError(tc.ExpEqualError), tc.CaseName)
		}

		if tc.ExpMatchError == "" {
			g.Expect(match(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(match(res)).To(gomega.MatchError(tc.ExpMatchError), tc.CaseName)
		}
	}
}

func Test_AssertBody(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Parallel()

	testCases := []struct {
		CaseName      string
		Body          []byte
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
			ExpEqualError: `body: expected "value", got "v"`,
			ExpMatchError: `body: expected to match "(?i)^val.*$", got "v"`,
		},
		{
			CaseName:      "Matching body",
			Body:          []byte("vAl"),
			ExpEqualError: `body: expected "value", got "vAl"`,
		},
		{
			CaseName: "Exact body",
			Body:     []byte("value"),
		},
	}

	equal := AssertBodyEqual("value")
	match := AssertBodyMatch(`(?i)^val.*$`)
	for _, tc := range testCases {
		res := &httpResponse{BodyBytes: tc.Body}

		if tc.ExpEqualError == "" {
			g.Expect(equal(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(equal(res)).To(gomega.MatchError(tc.ExpEqualError), tc.CaseName)
		}

		if tc.ExpMatchError == "" {
			g.Expect(match(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(match(res)).To(gomega.MatchError(tc.ExpMatchError), tc.CaseName)
		}
	}
}

func Test_AssertRedirect(t *testing.T) {
	g := gomega.NewWithT(t)

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
		res := &httpResponse{
			Response: &http.Response{
				StatusCode: tc.StatusCode,
				Status:     strings.Join(strings.Split(strconv.Itoa(tc.StatusCode), ""), "_"),
				Header:     http.Header(tc.Header),
			},
		}

		if tc.ExpEqualError == "" {
			g.Expect(equal(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(equal(res)).To(gomega.MatchError(tc.ExpEqualError), tc.CaseName)
		}

		if tc.ExpMatchError == "" {
			g.Expect(match(res)).To(gomega.BeNil(), tc.CaseName)
		} else {
			g.Expect(match(res)).To(gomega.MatchError(tc.ExpMatchError), tc.CaseName)
		}
	}
}
