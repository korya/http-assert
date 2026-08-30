package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	ha "github.com/korya/http-assert"
)

// response builds the shape Client.Do hands to the renderer: a real
// *http.Response plus the body already read off the wire.
func response(status string, header http.Header, body string) ha.Response {
	return ha.Response{
		Response: &http.Response{
			Proto:  "HTTP/1.1",
			Status: status,
			Header: header,
			// Set deliberately, and deliberately inconsistent with the body
			// below: the renderer must describe what it prints, not what the
			// transport said would arrive (#18).
			ContentLength:    int64(len(body)),
			TransferEncoding: []string{"chunked"},
		},
		BodyBytes: []byte(body),
	}
}

func Test_writeResponse(t *testing.T) {
	t.Parallel()

	plain := http.Header{"Content-Type": {"text/plain"}}

	tests := []struct {
		Name     string
		Response ha.Response
		WithBody bool
		Want     string
	}{{
		Name:     "status line, headers, body",
		Response: response("200 OK", plain, "hello"),
		WithBody: true,
		Want: "HTTP/1.1 200 OK\n" +
			"Content-Type: text/plain\n" +
			"\n" +
			"hello",
	}, {
		// The case #18 opens with: the placeholder is longer than the body it
		// stands in for, and used to be cut to the body's length.
		Name:     "omitted payload survives a shorter body",
		Response: response("500 Internal Server Error", plain, "boom"),
		WithBody: false,
		Want: "HTTP/1.1 500 Internal Server Error\n" +
			"Content-Type: text/plain\n" +
			"\n" +
			"  << Payload is omitted >>",
	}, {
		Name:     "headers are sorted and repeated values kept",
		Response: response("200 OK", http.Header{"Set-Cookie": {"a=1", "b=2"}, "Allow": {"GET"}}, ""),
		WithBody: true,
		Want: "HTTP/1.1 200 OK\n" +
			"Allow: GET\n" +
			"Set-Cookie: a=1\n" +
			"Set-Cookie: b=2\n" +
			"\n",
	}, {
		Name:     "no headers still leaves the blank separator",
		Response: response("204 No Content", http.Header{}, ""),
		WithBody: true,
		Want:     "HTTP/1.1 204 No Content\n\n",
	}, {
		// A body of raw bytes goes to the hex dumper, gutter and all -- the
		// whole point of the dumper, and what truncation used to remove.
		Name:     "binary body renders as a full hex dump",
		Response: response("200 OK", http.Header{}, "\x00\x01\x02\xff"),
		WithBody: true,
		Want: "HTTP/1.1 200 OK\n" +
			"\n" +
			"00000000  00 01 02 ff                                       |....|\n",
	}}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			var b strings.Builder
			writeResponse(&b, &tc.Response, tc.WithBody)

			if got := b.String(); got != tc.Want {
				t.Errorf("writeTo()\n got: %q\nwant: %q", got, tc.Want)
			}
		})
	}
}

// Test_writeResponseCrops covers the branch that reports hidden bytes.
// It is separate because the expected text depends on the crop limit.
func Test_writeResponseCrops(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", maxPayloadBytes+17)
	var b strings.Builder
	res := response("200 OK", http.Header{}, body)
	writeResponse(&b, &res, true)

	got := b.String()
	if want := strings.Repeat("x", maxPayloadBytes); !strings.Contains(got, want) {
		t.Errorf("writeTo() did not render the first %d bytes", maxPayloadBytes)
	}
	if want := "<< Payload is cropped: 17 bytes are hidden >>"; !strings.Contains(got, want) {
		t.Errorf("writeTo() = %q, want it to contain %q", got, want)
	}
}

// Test_writeTo_ignoresTransportFraming states the property #18 is really about:
// nothing describing how the body travelled may reach the reader. The response
// above advertises chunked transfer and a ContentLength that contradicts the
// rendering, and neither may show up.
func Test_writeTo_ignoresTransportFraming(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	res := response("200 OK", http.Header{"Content-Type": {"text/plain"}}, "boom")
	writeResponse(&b, &res, false)

	for _, unwanted := range []string{"chunked", "Transfer-Encoding", "\r"} {
		if strings.Contains(b.String(), unwanted) {
			t.Errorf("writeTo() leaked %q into the dump:\n%s", unwanted, b.String())
		}
	}
}

// Test_writeRequest covers the request half of the failure dump.
//
// http.Request.Write was used directly, and by then the transport had drained
// the body -- so the dump advertised a Content-Length with nothing behind it,
// which is both useless (the first question after a failed POST is what was
// sent) and self-contradictory as an HTTP message (#19).
func Test_writeRequest(t *testing.T) {
	t.Parallel()

	request := func(t *testing.T, method, body string) *http.Request {
		t.Helper()

		var r io.Reader = http.NoBody
		if body != "" {
			r = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, "http://example.com/things", r)
		if err != nil {
			t.Fatalf("cannot build the request: %s", err)
		}
		return req
	}

	// sent drains the body the way http.Client does, so the request under test
	// is in the state the dump actually receives.
	sent := func(t *testing.T, req *http.Request) *http.Request {
		t.Helper()

		attempt, err := cloneForAttempt(req)
		if err != nil {
			t.Fatalf("cannot clone: %s", err)
		}
		if _, err := io.ReadAll(attempt.Body); err != nil {
			t.Fatalf("cannot drain: %s", err)
		}
		_ = attempt.Body.Close()
		return attempt
	}

	t.Run("the body survives having been sent", func(t *testing.T) {
		req := sent(t, request(t, "POST", "PAYLOAD"))

		var b strings.Builder
		writeRequest(&b, req)

		out := b.String()
		if !strings.Contains(out, "PAYLOAD") {
			t.Errorf("the dump omits the body it says it sent:\n%s", out)
		}
		if !strings.Contains(out, "Content-Length: 7") {
			t.Errorf("the dump lost Content-Length:\n%s", out)
		}
	})

	t.Run("a request with no body dumps cleanly", func(t *testing.T) {
		req := sent(t, request(t, "GET", ""))

		var b strings.Builder
		writeRequest(&b, req)

		out := b.String()
		if !strings.Contains(out, "GET /things HTTP/1.1") {
			t.Errorf("the request line is missing:\n%s", out)
		}
		// No payload section, and nothing pretending there is one.
		if strings.Contains(out, "Payload is cropped") {
			t.Errorf("an empty body was reported as cropped:\n%s", out)
		}
	})

	// The other half of #18's lesson, which was applied to the response and
	// not to the request: a wire-format serializer puts the whole payload in
	// the report, however long it is.
	t.Run("a long body is cropped", func(t *testing.T) {
		body := strings.Repeat("x", maxPayloadBytes+44)
		req := sent(t, request(t, "POST", body))

		var b strings.Builder
		writeRequest(&b, req)

		out := b.String()
		if !strings.Contains(out, "<< Payload is cropped: 44 bytes are hidden >>") {
			t.Errorf("a %d-byte body was not cropped:\n%s", len(body), out)
		}
		// Counted in the body only -- the Host header carries an "x" of its own.
		_, payload, found := strings.Cut(out, "\r\n\r\n")
		if !found {
			t.Fatalf("no header/body separator in the dump:\n%s", out)
		}
		if n := strings.Count(payload, "x"); n != maxPayloadBytes {
			t.Errorf("%d body bytes reached the dump, want %d", n, maxPayloadBytes)
		}
	})

	t.Run("a non-printable body is hex-dumped", func(t *testing.T) {
		req := sent(t, request(t, "POST", "\xff\xfe\x07\x08"))

		var b strings.Builder
		writeRequest(&b, req)

		if out := b.String(); !strings.Contains(out, "ff fe 07 08") {
			t.Errorf("a binary body was not hex-dumped:\n%s", out)
		}
	})

	// Without GetBody there is nothing to replay. The dump must still render
	// the headers rather than failing outright.
	t.Run("a body that cannot be replayed still dumps its headers", func(t *testing.T) {
		req := sent(t, request(t, "POST", "PAYLOAD"))
		req.GetBody = nil

		var b strings.Builder
		writeRequest(&b, req)

		if out := b.String(); !strings.Contains(out, "POST /things HTTP/1.1") {
			t.Errorf("the headers went missing along with the body:\n%s", out)
		}
	})
}
