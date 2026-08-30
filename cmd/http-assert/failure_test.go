package main

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	ha "github.com/korya/http-assert"
)

func TestFormatFailure(t *testing.T) {
	t.Parallel()

	res := &ha.Response{Response: &http.Response{Status: "500 Internal Server Error"}}
	tests := []struct {
		name    string
		failure *ha.Failure
		want    string
	}{
		{"nil", nil, "assertion failed"},
		{"ok", &ha.Failure{Code: ha.FailureStatusOK, Actual: 500}, `ok: expected OK, got 500 ("500 Internal Server Error")`},
		{"nok", &ha.Failure{Code: ha.FailureStatusNOK, Actual: 200}, `nok: expected NOK, got 200 ("500 Internal Server Error")`},
		{"status", &ha.Failure{Code: ha.FailureStatus, Expected: "2xx", Actual: 500}, `status: expected 2xx, got 500 ("500 Internal Server Error")`},
		{"header present", &ha.Failure{Code: ha.FailureHeaderPresent, Target: "X-ID"}, `header[X-ID]: expected to be present, missing`},
		{"header missing", &ha.Failure{Code: ha.FailureHeaderMissing, Target: "X-ID", Actual: []string{"a", "b"}}, `header[X-ID]: expected to be missing, got "a", "b"`},
		{"header equal missing", &ha.Failure{Code: ha.FailureHeaderEqual, Target: "X-ID", Expected: "a"}, `header[X-ID]: expected "a", missing`},
		{"header equal differs", &ha.Failure{Code: ha.FailureHeaderEqual, Target: "X-ID", Expected: "a", Actual: []string{"b"}}, `header[X-ID]: expected "a", got "b"`},
		{"header match missing", &ha.Failure{Code: ha.FailureHeaderMatch, Target: "X-ID", Expected: "^a"}, `header[X-ID]: expected to match "^a", missing`},
		{"header match differs", &ha.Failure{Code: ha.FailureHeaderMatch, Target: "X-ID", Expected: "^a", Actual: []string{"b"}}, `header[X-ID]: expected to match "^a", got "b"`},
		{"body empty", &ha.Failure{Code: ha.FailureBodyEmpty, Actual: "body"}, `body: expected to be empty, got "body"`},
		{"body nonempty", &ha.Failure{Code: ha.FailureBodyNotEmpty}, `body: expected to be non-empty, got nothing`},
		{"body equal missing", &ha.Failure{Code: ha.FailureBodyEqual, Expected: "body"}, `body: expected "body", missing`},
		{"body equal differs", &ha.Failure{Code: ha.FailureBodyEqual, Expected: "want", Actual: "got"}, `body: expected "want", got "got"`},
		{"body match missing", &ha.Failure{Code: ha.FailureBodyMatch, Expected: "^body$"}, `body: expected to match "^body$", missing`},
		{"body match differs", &ha.Failure{Code: ha.FailureBodyMatch, Expected: "^want$", Actual: "got"}, `body: expected to match "^want$", got "got"`},
		{"jq value", &ha.Failure{Code: ha.FailureJQValue, Target: ".ok", Actual: false}, `jq[.ok]: expected true, got false`},
		{"jq no output", &ha.Failure{Code: ha.FailureJQNoOutput, Target: ".items[]"}, `jq[.items[]]: expected true, got no output`},
		{"redirect status", &ha.Failure{Code: ha.FailureRedirectStatus, Actual: 500}, `redirect: wrong HTTP status: got 500 ("500 Internal Server Error")`},
		{"redirect absent", &ha.Failure{Code: ha.FailureRedirectLocationAbsent}, `redirect: no Location header`},
		{"redirect equal", &ha.Failure{Code: ha.FailureRedirectEqual, Expected: "/want", Actual: "/got"}, `redirect: wrong Location: expected "/want", got "/got"`},
		{"redirect match", &ha.Failure{Code: ha.FailureRedirectMatch, Expected: "^/want", Actual: "/got"}, `redirect: wrong Location: expected to match "^/want", got "/got"`},
		{"unknown", &ha.Failure{Kind: "custom", Code: "custom", Expected: 1, Actual: 2}, `custom assertion failed: expected 1, got 2`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatFailure(tc.failure, res); got != tc.want {
				t.Errorf("formatFailure() = %q, want %q", got, tc.want)
			}
		})
	}

	if got := responseStatus(nil); got != "" {
		t.Errorf("responseStatus(nil) = %q", got)
	}
	if got := responseStatus(&ha.Response{}); got != "" {
		t.Errorf("responseStatus(empty) = %q", got)
	}
	if got := headerValues("odd"); got != `"odd"` {
		t.Errorf("headerValues(fallback) = %q", got)
	}
	unencodable := make(chan int)
	if got, want := jqValue(unencodable), fmt.Sprint(unencodable); got != want {
		t.Errorf("jqValue(unencodable) = %q, want %q", got, want)
	}
}

func TestFormatEvaluationError(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"plain", cause, "cause"},
		{"body decode", &ha.EvaluationError{Code: ha.EvaluationBodyDecode, Encoding: "gzip", Cause: cause}, "body: response is gzip-encoded and was not decoded: cause"},
		{"json", &ha.EvaluationError{Code: ha.EvaluationJSON, Cause: cause}, "body: expected JSON, got cause"},
		{"jq", &ha.EvaluationError{Code: ha.EvaluationJQ, Target: ".ok", Cause: cause}, "jq[.ok]: cause"},
		{"unknown", &ha.EvaluationError{Code: "custom", Cause: cause}, "cause"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatEvaluationError(tc.err); got != tc.want {
				t.Errorf("formatEvaluationError() = %q, want %q", got, tc.want)
			}
		})
	}
}
