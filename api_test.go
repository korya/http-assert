package httpassert_test

import (
	"fmt"
	"net/http"

	ha "github.com/korya/http-assert"
)

type exampleTransport func(*http.Request) (*http.Response, error)

func (f exampleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func ExampleClient_Do() {
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/health", nil)
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
