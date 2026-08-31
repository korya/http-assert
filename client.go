package httpassert

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"
)

const defaultRequestTimeout = 20 * time.Second

// defaultHTTPClient is shared so its underlying DefaultTransport can reuse
// connections. Its total timeout covers connection setup, redirects, and
// reading the response body; callers needing another policy supply HTTPClient.
var defaultHTTPClient = &http.Client{Timeout: defaultRequestTimeout}

var (
	// ErrNoAssertions is returned before a request is sent when Do has nothing
	// to check.
	ErrNoAssertions = errors.New("no assertions defined")
	// ErrNilRequest is returned when Do receives a nil request.
	ErrNilRequest = errors.New("request is nil")
	// ErrNilAssertion is returned before a request is sent when an assertion is
	// nil.
	ErrNilAssertion = errors.New("assertion is nil")
)

// EvaluationErrorCode identifies why an assertion could not be evaluated.
type EvaluationErrorCode string

const (
	EvaluationBodyDecode EvaluationErrorCode = "body_decode"
	EvaluationJSON       EvaluationErrorCode = "json_decode"
	EvaluationJQ         EvaluationErrorCode = "jq_evaluation"
)

// EvaluationError describes an assertion that could not reach a verdict. This
// differs from Failure: a Failure means the response was evaluated and did not
// satisfy the assertion.
type EvaluationError struct {
	Code     EvaluationErrorCode
	Kind     AssertionKind // assertion family; Client and built-in assertions populate it
	Target   string        // jq query or "" when the evaluation needs no subject
	Encoding string        // response content encoding for body-decode errors
	Cause    error         // underlying decoder, JSON, context, or jq error
}

func (e *EvaluationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

// Unwrap exposes the underlying decoder, JSON, or jq error.
func (e *EvaluationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Outcome is the result of evaluating one assertion. Client.Do guarantees that
// Failure and Err are not both set; an error from a custom assertion takes
// precedence over a simultaneously returned failure. Passed reports whether
// both are nil.
type Outcome struct {
	Kind    AssertionKind
	Failure *Failure
	Err     error
}

func (o Outcome) Passed() bool { return o.Failure == nil && o.Err == nil }

// Result contains the received response and one outcome per assertion, in the
// order supplied to Client.Do.
type Result struct {
	Response *Response
	Outcomes []Outcome
}

// Passed reports whether at least one assertion was evaluated and every
// assertion held.
func (r *Result) Passed() bool {
	if r == nil || len(r.Outcomes) == 0 {
		return false
	}
	for _, outcome := range r.Outcomes {
		if !outcome.Passed() {
			return false
		}
	}
	return true
}

// Client invokes an HTTP client and evaluates assertions against its response.
// It never retries; retry policy, logging, and presentation belong to the
// caller. The configured HTTP client's redirect policy still applies.
//
// The zero value uses a shared client with a 20-second total request timeout.
// Set HTTPClient when the caller needs custom transports, redirect behavior,
// TLS settings, or timeouts.
type Client struct {
	HTTPClient *http.Client
}

// Do calls the configured HTTP client's Do method once. A non-nil error means
// no complete response was available for assertions. Assertion failures and
// evaluation errors are returned as structured Outcomes with a nil top-level
// error. When reading the response body fails, Result contains the partial
// response and no outcomes alongside the non-nil error.
func (c Client) Do(req *http.Request, assertions ...Assertion) (*Result, error) {
	if req == nil {
		return nil, ErrNilRequest
	}
	if len(assertions) == 0 {
		return nil, ErrNoAssertions
	}
	for i, assertion := range assertions {
		if isNilAssertion(assertion) {
			return nil, fmt.Errorf("%w at index %d", ErrNilAssertion, i)
		}
	}

	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}

	// The caller supplies both the request and (optionally) the HTTP client;
	// issuing that request is the package's contract. Applications accepting
	// untrusted URLs must enforce their own destination policy before this API.
	res, err := client.Do(req) // #nosec G704 -- this API intentionally sends its caller's request
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	httpRes := &Response{Response: res}
	httpRes.BodyBytes, err = io.ReadAll(res.Body)
	result := &Result{Response: httpRes}
	if err != nil {
		return result, fmt.Errorf("read response body: %w", err)
	}
	httpRes.decodeBody()

	result.Outcomes = make([]Outcome, 0, len(assertions))
	for _, assertion := range assertions {
		failure, checkErr := assertion.Check(httpRes)
		kind := assertion.Kind()
		failure, checkErr = normalizeOutcome(kind, failure, checkErr)
		result.Outcomes = append(result.Outcomes, Outcome{
			Kind:    kind,
			Failure: failure,
			Err:     checkErr,
		})
	}

	return result, nil
}

func isNilAssertion(assertion Assertion) bool {
	if assertion == nil {
		return true
	}

	value := reflect.ValueOf(assertion)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
