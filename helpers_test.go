package httpassert

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type textError string

func (e textError) Error() string { return string(e) }

// response builds the shape Client.Do hands to assertions: the standard
// response metadata plus a body already read off the wire.
func response(status string, header http.Header, body string) Response {
	return Response{
		Response: &http.Response{
			Proto:  "HTTP/1.1",
			Status: status,
			Header: header,
		},
		BodyBytes: []byte(body),
	}
}

// checkErr asserts that err says exactly what the caller expects. An empty want
// means "no error at all".
//
// This is the shape almost every assertion table in this package needs: each
// case names the error it expects, or leaves it blank to mean success. Note
// that a nil error is a failure whenever want is non-empty -- forgetting that
// is how an assertion silently stops asserting anything.
func checkErr(t *testing.T, label string, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Errorf("%s: unexpected error: %s", label, err)
		}
		return
	}

	if err == nil {
		t.Errorf("%s: expected error %q, got nil", label, want)
		return
	}

	if got := testErrorText(err); got != want {
		t.Errorf("%s: error = %q, want %q", label, got, want)
	}
}

// checkErrMatch is checkErr for the one case whose message contains a value
// that is not worth pinning exactly.
func checkErrMatch(t *testing.T, label string, err error, pattern string) {
	t.Helper()

	if err == nil {
		t.Errorf("%s: expected error matching %q, got nil", label, pattern)
		return
	}

	got := testErrorText(err)
	if ok, reErr := regexp.MatchString(pattern, got); reErr != nil {
		t.Fatalf("%s: bad pattern %q: %s", label, pattern, reErr)
	} else if !ok {
		t.Errorf("%s: error = %q, want match %q", label, got, pattern)
	}
}

// check runs an assertion the way doOnce does, flattening Check's two returns
// into the single error these tables compare against.
//
// The nil guard is load-bearing: returning a nil *Failure through an error
// interface yields a non-nil error holding a nil pointer, which would fail
// every "expected no error" case with an unreadable message.
func check(a Assertion, res *Response) error {
	f, err := a.Check(res)
	if err != nil {
		return err
	}
	if f != nil {
		return textError(testFailureText(f, res))
	}

	return nil
}

// These assertion tables predate the reusable API and pin the CLI's wording.
// Deriving the text from the new structured failure proves that every former
// message remains representable without storing presentation in Failure.
func testFailureText(f *Failure, res *Response) string {
	expected := fmt.Sprint(f.Expected)
	switch f.Code {
	case FailureStatusOK:
		return fmt.Sprintf("ok: expected OK, got %v (%q)", f.Actual, res.Status)
	case FailureStatusNOK:
		return fmt.Sprintf("nok: expected NOK, got %v (%q)", f.Actual, res.Status)
	case FailureStatus:
		return fmt.Sprintf("status: expected %s, got %v (%q)", expected, f.Actual, res.Status)
	case FailureHeaderPresent:
		return fmt.Sprintf("header[%s]: expected to be present, missing", f.Target)
	case FailureHeaderMissing:
		return fmt.Sprintf("header[%s]: expected to be missing, got %s", f.Target, testHeaderValues(f.Actual))
	case FailureHeaderEqual:
		if f.Actual == nil {
			return fmt.Sprintf("header[%s]: expected %q, missing", f.Target, expected)
		}
		return fmt.Sprintf("header[%s]: expected %q, got %s", f.Target, expected, testHeaderValues(f.Actual))
	case FailureHeaderMatch:
		if f.Actual == nil {
			return fmt.Sprintf("header[%s]: expected to match %q, missing", f.Target, expected)
		}
		return fmt.Sprintf("header[%s]: expected to match %q, got %s", f.Target, expected, testHeaderValues(f.Actual))
	case FailureBodyEmpty:
		return fmt.Sprintf("body: expected to be empty, got %q", f.Actual)
	case FailureBodyNotEmpty:
		return "body: expected to be non-empty, got nothing"
	case FailureBodyEqual:
		if f.Actual == nil {
			return fmt.Sprintf("body: expected %q, missing", expected)
		}
		return fmt.Sprintf("body: expected %q, got %q", expected, f.Actual)
	case FailureBodyMatch:
		if f.Actual == nil {
			return fmt.Sprintf("body: expected to match %q, missing", expected)
		}
		return fmt.Sprintf("body: expected to match %q, got %q", expected, f.Actual)
	case FailureJQValue:
		return fmt.Sprintf("jq[%s]: expected true, got %s", f.Target, testJQValue(f.Actual))
	case FailureJQNoOutput:
		return fmt.Sprintf("jq[%s]: expected true, got no output", f.Target)
	case FailureRedirectStatus:
		return fmt.Sprintf("redirect: wrong HTTP status: got %v (%q)", f.Actual, res.Status)
	case FailureRedirectLocationAbsent:
		return "redirect: no Location header"
	case FailureRedirectEqual:
		return fmt.Sprintf("redirect: wrong Location: expected %q, got %q", expected, f.Actual)
	case FailureRedirectMatch:
		return fmt.Sprintf("redirect: wrong Location: expected to match %q, got %q", expected, f.Actual)
	default:
		return fmt.Sprintf("unknown failure code %q", f.Code)
	}
}

func testErrorText(err error) string {
	var evaluation *EvaluationError
	if !errors.As(err, &evaluation) {
		return err.Error()
	}
	switch evaluation.Code {
	case EvaluationBodyDecode:
		return fmt.Sprintf("body: response is %s-encoded and was not decoded: %s", evaluation.Encoding, evaluation.Cause)
	case EvaluationJSON:
		return fmt.Sprintf("body: expected JSON, got %s", evaluation.Cause)
	case EvaluationJQ:
		return fmt.Sprintf("jq[%s]: %s", evaluation.Target, evaluation.Cause)
	default:
		return err.Error()
	}
}

func testHeaderValues(value any) string {
	values := value.([]string)
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return strings.Join(quoted, ", ")
}

func testJQValue(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(b)
}
