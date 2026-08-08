package main

import (
	"net/http"
	"strings"
	"testing"
)

// response builds the shape Client.Do hands to the renderer: a real
// *http.Response plus the body already read off the wire.
func response(status string, header http.Header, body string) httpResponse {
	return httpResponse{
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

func Test_httpResponse_writeTo(t *testing.T) {
	t.Parallel()

	plain := http.Header{"Content-Type": {"text/plain"}}

	tests := []struct {
		Name     string
		Response httpResponse
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
			tc.Response.writeTo(&b, tc.WithBody)

			if got := b.String(); got != tc.Want {
				t.Errorf("writeTo()\n got: %q\nwant: %q", got, tc.Want)
			}
		})
	}
}

// Test_httpResponse_writeToCrops covers the branch that reports hidden bytes.
// It is separate because the expected text depends on the crop limit.
func Test_httpResponse_writeToCrops(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", maxPayloadBytes+17)
	var b strings.Builder
	response("200 OK", http.Header{}, body).writeTo(&b, true)

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
	response("200 OK", http.Header{"Content-Type": {"text/plain"}}, "boom").
		writeTo(&b, false)

	for _, unwanted := range []string{"chunked", "Transfer-Encoding", "\r"} {
		if strings.Contains(b.String(), unwanted) {
			t.Errorf("writeTo() leaked %q into the dump:\n%s", unwanted, b.String())
		}
	}
}
