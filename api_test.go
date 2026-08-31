package httpassert_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ha "github.com/korya/http-assert"
)

type exampleTransport func(*http.Request) (*http.Response, error)

func (f exampleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type customAssertion struct{}

func (customAssertion) Kind() ha.AssertionKind { return "custom" }

func (customAssertion) Check(*ha.Response) (*ha.Failure, error) { return nil, nil }

var _ ha.Assertion = customAssertion{}

func ExampleClient_Do() {
	req := ha.Must(http.NewRequest(http.MethodGet, "https://example.test/health", nil))
	client := ha.Client{HTTPClient: &http.Client{Transport: exampleTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Status:     "204 No Content",
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})}}

	result, err := client.Do(req, ha.AssertStatusOK(), ha.AssertBodyEmpty())
	fmt.Println(err)
	fmt.Println(result.Passed())

	// Output:
	// <nil>
	// true
}

func ExampleMust() {
	req := ha.Must(http.NewRequest(http.MethodGet, "https://example.test/health", nil))
	assertion := ha.Must(ha.AssertJQ(`.status == "healthy"`))
	fmt.Println(req.Method)
	fmt.Println(assertion.Kind())

	// Output:
	// GET
	// jq
}

func ExampleOutcome() {
	req := ha.Must(http.NewRequest(http.MethodGet, "https://example.test/health", nil))
	client := ha.Client{HTTPClient: &http.Client{Transport: exampleTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("not JSON")),
			Request:    req,
		}, nil
	})}}

	result, err := client.Do(req, ha.AssertStatusOK(), ha.Must(ha.AssertJQ(".healthy")))
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, outcome := range result.Outcomes {
		switch {
		case outcome.Err != nil:
			var evaluation *ha.EvaluationError
			if errors.As(outcome.Err, &evaluation) {
				fmt.Printf("%s could not be evaluated: %s\n", outcome.Kind, evaluation.Code)
			} else {
				fmt.Printf("%s could not be evaluated: %v\n", outcome.Kind, outcome.Err)
			}
		case outcome.Failure != nil:
			fmt.Printf("%s failed: %s (got %v)\n", outcome.Kind, outcome.Failure.Code, outcome.Failure.Actual)
		}
	}

	// Output:
	// ok failed: status_ok (got 500)
	// jq could not be evaluated: json_decode
}

func ExampleClient_customHTTPPolicy() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	req := ha.Must(http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test/health", nil))
	client := ha.Client{HTTPClient: &http.Client{
		Timeout: 3 * time.Second,
		Transport: exampleTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("healthy")),
				Request:    req,
			}, nil
		}),
	}}

	result, err := client.Do(req, ha.AssertStatusOK())
	fmt.Println(err)
	fmt.Println(string(result.Response.BodyBytes))

	// Output:
	// <nil>
	// healthy
}
