package httpassert

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type testAssertion struct {
	kind  AssertionKind
	check func(*Response) (*Failure, error)
}

func (a testAssertion) Kind() AssertionKind { return a.kind }

func (a testAssertion) Check(res *Response) (*Failure, error) {
	return a.check(res)
}

type pointerAssertion struct{}

func (*pointerAssertion) Kind() AssertionKind { return "pointer" }

func (*pointerAssertion) Check(*Response) (*Failure, error) { return nil, nil }

type trackingBody struct {
	reader io.Reader
	closed bool
}

func (b *trackingBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

type failingReader struct {
	done bool
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, "partial"), nil
	}
	return 0, r.err
}

func request(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.test/health", nil)
	if err != nil {
		t.Fatalf("NewRequest: %s", err)
	}
	return req
}

func TestClientDoRejectsInvalidInputBeforeSending(t *testing.T) {
	sent := 0
	client := Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		sent++
		return nil, errors.New("must not send")
	})}}

	if result, err := client.Do(nil, AssertStatusOK()); result != nil || !errors.Is(err, ErrNilRequest) {
		t.Errorf("nil request = (%v, %v), want (nil, ErrNilRequest)", result, err)
	}
	if result, err := client.Do(request(t)); result != nil || !errors.Is(err, ErrNoAssertions) {
		t.Errorf("no assertions = (%v, %v), want (nil, ErrNoAssertions)", result, err)
	}
	if result, err := client.Do(request(t), nil); result != nil || !errors.Is(err, ErrNilAssertion) {
		t.Errorf("nil assertion = (%v, %v), want (nil, ErrNilAssertion)", result, err)
	}
	var typedNil *pointerAssertion
	if result, err := client.Do(request(t), typedNil); result != nil || !errors.Is(err, ErrNilAssertion) {
		t.Errorf("typed-nil assertion = (%v, %v), want (nil, ErrNilAssertion)", result, err)
	}
	if sent != 0 {
		t.Errorf("transport called %d times for invalid input", sent)
	}
}

func TestClientDoPerformsOneRequestAndChecksEveryAssertionInOrder(t *testing.T) {
	body := &trackingBody{reader: strings.NewReader("payload")}
	sent := 0
	client := Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sent++
		return &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Proto:      "HTTP/1.1",
			Header:     http.Header{"X-Service": {"ready"}},
			Body:       body,
			Request:    req,
		}, nil
	})}}

	var checked []string
	evaluationCause := errors.New("cannot decide")
	assertions := []Assertion{
		testAssertion{kind: "first", check: func(res *Response) (*Failure, error) {
			checked = append(checked, "first")
			if string(res.BodyBytes) != "payload" {
				t.Errorf("first assertion saw body %q", res.BodyBytes)
			}
			return nil, nil
		}},
		testAssertion{kind: "second", check: func(*Response) (*Failure, error) {
			checked = append(checked, "second")
			return &Failure{Code: FailureBodyEqual, Expected: "want", Actual: "got"}, nil
		}},
		testAssertion{kind: "third", check: func(*Response) (*Failure, error) {
			checked = append(checked, "third")
			return nil, evaluationCause
		}},
	}

	result, err := client.Do(request(t), assertions...)
	if err != nil {
		t.Fatalf("Do: %s", err)
	}
	if sent != 1 {
		t.Errorf("requests sent = %d, want 1", sent)
	}
	if got := strings.Join(checked, ","); got != "first,second,third" {
		t.Errorf("assertion order = %q", got)
	}
	if !body.closed {
		t.Error("response body was not closed")
	}
	if result.Response.StatusCode != 200 || string(result.Response.BodyBytes) != "payload" {
		t.Errorf("Response = %+v", result.Response)
	}
	if len(result.Outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(result.Outcomes))
	}
	if !result.Outcomes[0].Passed() {
		t.Errorf("first outcome = %+v, want pass", result.Outcomes[0])
	}
	if result.Outcomes[1].Failure == nil || result.Outcomes[1].Failure.Kind != "second" {
		t.Errorf("second outcome = %+v, want stamped Failure", result.Outcomes[1])
	}
	if !errors.Is(result.Outcomes[2].Err, evaluationCause) {
		t.Errorf("third outcome error = %v, want %v", result.Outcomes[2].Err, evaluationCause)
	}
	if result.Passed() {
		t.Error("Result.Passed() = true with failed outcomes")
	}
}

func TestClientDoNormalizesCustomAssertionOutcome(t *testing.T) {
	cause := errors.New("cannot decide")
	assertion := testAssertion{kind: "custom", check: func(*Response) (*Failure, error) {
		return &Failure{Code: FailureBodyEqual}, &EvaluationError{
			Code:  EvaluationJQ,
			Kind:  "wrong",
			Cause: cause,
		}
	}}
	client := Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})}}

	result, err := client.Do(request(t), assertion)
	if err != nil {
		t.Fatalf("Do: %s", err)
	}
	outcome := result.Outcomes[0]
	if outcome.Failure != nil {
		t.Errorf("Failure = %+v, want nil when assertion also returned an error", outcome.Failure)
	}
	var evaluation *EvaluationError
	if !errors.As(outcome.Err, &evaluation) {
		t.Fatalf("Err = %T %v, want *EvaluationError", outcome.Err, outcome.Err)
	}
	if evaluation.Kind != assertion.Kind() || !errors.Is(evaluation, cause) {
		t.Errorf("EvaluationError = %+v, want kind %q wrapping cause", evaluation, assertion.Kind())
	}
}

func TestClientDoUsesPackageDefaultClient(t *testing.T) {
	original := defaultHTTPClient
	t.Cleanup(func() { defaultHTTPClient = original })

	called := false
	defaultHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: 204,
			Status:     "204 No Content",
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})}

	result, err := (Client{}).Do(request(t), AssertStatusOK(), AssertBodyEmpty())
	if err != nil {
		t.Fatalf("Do: %s", err)
	}
	if !called || !result.Passed() {
		t.Errorf("called = %v, result = %+v", called, result)
	}
}

func TestDefaultHTTPClientBoundsTheWholeRequest(t *testing.T) {
	if got := defaultHTTPClient.Timeout; got != defaultRequestTimeout {
		t.Errorf("default timeout = %s, want %s", got, defaultRequestTimeout)
	}
	if defaultHTTPClient.Timeout <= 0 {
		t.Error("default client has no total request timeout")
	}
}

func TestClientDoReturnsTransportError(t *testing.T) {
	want := errors.New("network unavailable")
	client := Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})}}

	result, err := client.Do(request(t), AssertStatusOK())
	if result != nil || !errors.Is(err, want) {
		t.Errorf("Do = (%v, %v), want (nil, transport error)", result, err)
	}
}

func TestClientDoReturnsPartialResponseOnReadError(t *testing.T) {
	want := errors.New("body interrupted")
	body := &trackingBody{reader: &failingReader{err: want}}
	client := Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       body,
			Request:    req,
		}, nil
	})}}

	result, err := client.Do(request(t), AssertStatusOK())
	if result == nil || result.Response == nil {
		t.Fatal("Do returned no partial response")
	}
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want wrapped read error", err)
	}
	if got := string(result.Response.BodyBytes); got != "partial" {
		t.Errorf("partial body = %q", got)
	}
	if result.Response.DecodeErr != nil {
		t.Errorf("DecodeErr = %v, want nil because decoding was not attempted", result.Response.DecodeErr)
	}
	if !body.closed {
		t.Error("response body was not closed after read error")
	}
	if result.Passed() {
		t.Error("an incomplete result must not pass")
	}
}

func TestResultAndEvaluationErrorHelpers(t *testing.T) {
	if (*Result)(nil).Passed() {
		t.Error("nil result passed")
	}
	if (&Result{}).Passed() {
		t.Error("unchecked result passed")
	}
	if !(Outcome{}).Passed() {
		t.Error("empty outcome should represent a pass")
	}
	if (Outcome{Failure: &Failure{}}).Passed() || (Outcome{Err: errors.New("x")}).Passed() {
		t.Error("failed outcomes passed")
	}

	cause := errors.New("decoder broke")
	err := &EvaluationError{Code: EvaluationBodyDecode, Cause: cause}
	if err.Error() != cause.Error() || !errors.Is(err, cause) || err.Unwrap() != cause {
		t.Errorf("EvaluationError did not expose cause: %v", err)
	}
	err = &EvaluationError{Code: EvaluationJSON}
	if err.Error() != string(EvaluationJSON) || err.Unwrap() != nil {
		t.Errorf("cause-less EvaluationError = %q, unwrap %v", err.Error(), err.Unwrap())
	}
	var nilErr *EvaluationError
	if nilErr.Error() != "<nil>" || nilErr.Unwrap() != nil {
		t.Errorf("nil EvaluationError = %q, unwrap %v", nilErr.Error(), nilErr.Unwrap())
	}
}
