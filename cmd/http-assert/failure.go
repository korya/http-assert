package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	ha "github.com/korya/http-assert"
)

// formatFailure is deliberately part of the command, not the library. The
// library reports stable fields; this artifact owns the human wording and can
// evolve it without making presentation part of the public API contract.
func formatFailure(f *ha.Failure, res *ha.Response) string {
	if f == nil {
		return "assertion failed"
	}

	expected := fmt.Sprint(f.Expected)
	switch f.Code {
	case ha.FailureStatusOK:
		return fmt.Sprintf("ok: expected OK, got %v (%q)", f.Actual, responseStatus(res))
	case ha.FailureStatusNOK:
		return fmt.Sprintf("nok: expected NOK, got %v (%q)", f.Actual, responseStatus(res))
	case ha.FailureStatus:
		return fmt.Sprintf("status: expected %s, got %v (%q)", expected, f.Actual, responseStatus(res))
	case ha.FailureHeaderPresent:
		return fmt.Sprintf("header[%s]: expected to be present, missing", f.Target)
	case ha.FailureHeaderMissing:
		return fmt.Sprintf("header[%s]: expected to be missing, got %s", f.Target, headerValues(f.Actual))
	case ha.FailureHeaderEqual:
		if f.Actual == nil {
			return fmt.Sprintf("header[%s]: expected %q, missing", f.Target, expected)
		}
		return fmt.Sprintf("header[%s]: expected %q, got %s", f.Target, expected, headerValues(f.Actual))
	case ha.FailureHeaderMatch:
		if f.Actual == nil {
			return fmt.Sprintf("header[%s]: expected to match %q, missing", f.Target, expected)
		}
		return fmt.Sprintf("header[%s]: expected to match %q, got %s", f.Target, expected, headerValues(f.Actual))
	case ha.FailureBodyEmpty:
		return fmt.Sprintf("body: expected to be empty, got %q", f.Actual)
	case ha.FailureBodyNotEmpty:
		return "body: expected to be non-empty, got nothing"
	case ha.FailureBodyEqual:
		if f.Actual == nil {
			return fmt.Sprintf("body: expected %q, missing", expected)
		}
		return fmt.Sprintf("body: expected %q, got %q", expected, f.Actual)
	case ha.FailureBodyMatch:
		if f.Actual == nil {
			return fmt.Sprintf("body: expected to match %q, missing", expected)
		}
		return fmt.Sprintf("body: expected to match %q, got %q", expected, f.Actual)
	case ha.FailureJQValue:
		return fmt.Sprintf("jq[%s]: expected true, got %s", f.Target, jqValue(f.Actual))
	case ha.FailureJQNoOutput:
		return fmt.Sprintf("jq[%s]: expected true, got no output", f.Target)
	case ha.FailureRedirectStatus:
		return fmt.Sprintf("redirect: wrong HTTP status: got %v (%q)", f.Actual, responseStatus(res))
	case ha.FailureRedirectLocationAbsent:
		return "redirect: no Location header"
	case ha.FailureRedirectEqual:
		return fmt.Sprintf("redirect: wrong Location: expected %q, got %q", expected, f.Actual)
	case ha.FailureRedirectMatch:
		return fmt.Sprintf("redirect: wrong Location: expected to match %q, got %q", expected, f.Actual)
	default:
		return fmt.Sprintf("%s assertion failed: expected %v, got %v", f.Kind, f.Expected, f.Actual)
	}
}

func formatEvaluationError(err error) string {
	var evaluation *ha.EvaluationError
	if !errors.As(err, &evaluation) {
		return err.Error()
	}

	switch evaluation.Code {
	case ha.EvaluationBodyDecode:
		return fmt.Sprintf("body: response is %s-encoded and was not decoded: %s",
			evaluation.Encoding, evaluation.Cause)
	case ha.EvaluationJSON:
		return fmt.Sprintf("body: expected JSON, got %s", evaluation.Cause)
	case ha.EvaluationJQ:
		return fmt.Sprintf("jq[%s]: %s", evaluation.Target, evaluation.Cause)
	default:
		return err.Error()
	}
}

func responseStatus(res *ha.Response) string {
	if res == nil || res.Response == nil {
		return ""
	}
	return res.Status
}

func headerValues(value any) string {
	values, ok := value.([]string)
	if !ok {
		return fmt.Sprintf("%q", value)
	}

	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return strings.Join(quoted, ", ")
}

func jqValue(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(b)
}
