package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// cloneForAttempt is the piece of retrying that cannot be observed from the
// outside once it works, and cannot be observed at all once it does not: a
// second attempt that quietly sends an empty body still gets a response, and
// the run goes green having posted nothing.
func Test_cloneForAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name string
		Body io.Reader
		Want string
	}{
		{
			// What the CLI actually builds for -d. http.NewRequest recognises
			// the type and installs GetBody, which is what makes the replay
			// possible at all.
			Name: "a string body is replayed",
			Body: strings.NewReader("payload"),
			Want: "payload",
		},
		{
			// What the CLI builds without -d. There is no GetBody here, and
			// none is needed: http.NoBody re-reads as empty forever.
			Name: "no body stays no body",
			Body: http.NoBody,
			Want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			req, err := http.NewRequest("POST", "http://example.com/", tc.Body)
			if err != nil {
				t.Fatalf("cannot build the request: %s", err)
			}
			req.Header.Set("X-Probe", "1")

			// Twice, because the first attempt is the one that drains the
			// original. A clone that works only before that is no use.
			for attempt := 1; attempt <= 2; attempt++ {
				c, err := cloneForAttempt(req)
				if err != nil {
					t.Fatalf("attempt %d: %s", attempt, err)
				}

				body, err := io.ReadAll(c.Body)
				if err != nil {
					t.Fatalf("attempt %d: cannot read the body: %s", attempt, err)
				}
				_ = c.Body.Close()

				if got := string(body); got != tc.Want {
					t.Errorf("attempt %d: body = %q, want %q", attempt, got, tc.Want)
				}
				if got, want := c.Method, "POST"; got != want {
					t.Errorf("attempt %d: method = %q, want %q", attempt, got, want)
				}
				if got, want := c.Header.Get("X-Probe"), "1"; got != want {
					t.Errorf("attempt %d: X-Probe = %q, want %q", attempt, got, want)
				}
			}
		})
	}
}

// Test_cloneForAttempt_isolatesHeaders: the clone must not share the header map
// with the request it came from, or a header added by one attempt would still
// be there on the next.
func Test_cloneForAttempt_isolatesHeaders(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest("GET", "http://example.com/", http.NoBody)
	if err != nil {
		t.Fatalf("cannot build the request: %s", err)
	}

	c, err := cloneForAttempt(req)
	if err != nil {
		t.Fatalf("cannot clone: %s", err)
	}
	c.Header.Set("X-Added-By-This-Attempt", "1")

	if got := req.Header.Get("X-Added-By-This-Attempt"); got != "" {
		t.Errorf("the original gained a header from its clone: %q", got)
	}
}
