package main_test

import (
	"strings"
	"testing"
)

// The failure dump is the only thing a person reads when an assertion fails,
// so what it contains is part of the contract.
//
// It used to be produced by http.Response.Write, an HTTP wire serializer. That
// honours Content-Length and Transfer-Encoding, which describe the body that
// arrived rather than the rendering that replaces it -- so a rendering longer
// than the original body was cut to the original's length, and a chunked
// response had its framing interleaved with the text (#18). Each test below
// failed before that was fixed.

func TestE2EFailureDump(t *testing.T) {
	// /500 returns a four-byte body, which used to cut the twenty-five byte
	// placeholder down to "  <<".
	t.Run("omitted placeholder is not cut to the body length", func(t *testing.T) {
		r := run(t, nil, "-s", "--assert-ok", url("/500"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "  << Payload is omitted >>")
	})

	// Eight raw bytes used to render as the offset "00000000" and nothing
	// else, from a dumper whose entire purpose is showing the bytes.
	t.Run("binary body keeps its hex dump and ascii gutter", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", "never-matches", url("/binary"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "00000000")
		assertContains(t, r, "00 01 02 03 ff fe 07 08")
		assertContains(t, r, "|")
	})

	// A chunked response has no Content-Length, so instead of truncating, the
	// serializer re-framed the rendering: a hex length, then the text, then a
	// zero terminator, plus a Transfer-Encoding header it invented.
	t.Run("chunked response is dumped without transfer framing", func(t *testing.T) {
		r := run(t, nil, "-v", "--assert-body-eq", "never-matches", url("/chunked"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "first chunk second chunk")
		assertNotContains(t, r, "Transfer-Encoding")

		// "18" is the chunk length the serializer used to emit for this body.
		// Anchored to line starts so a chunk length is not confused with the
		// same digits inside a header value or a timing.
		dump, _, _ := strings.Cut(r.Output(), "first chunk")
		for line := range strings.SplitSeq(dump, "\n") {
			if strings.TrimSpace(line) == "18" || strings.TrimSpace(line) == "0" {
				t.Fatalf("chunk framing leaked into the dump:\n%s", r.Output())
			}
		}
	})

	// unicode.IsPrint answers false for '\n', so a body with a line break in it
	// -- pretty-printed JSON, HTML, a log excerpt -- was shown as a hex dump.
	t.Run("multi-line text body is shown as text", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", "never-matches", url("/multiline"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "\"status\": \"success\"")
		assertNotContains(t, r, "00000000")
	})

	// Cropping is a property of the renderer, not of the transport, and has to
	// survive the change.
	t.Run("large body is still cropped and says so", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", "never-matches", url("/big"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "Payload is cropped")
		assertContains(t, r, "bytes are hidden")
	})

	// Wire line endings in a human-readable report show up as ^M in pagers and
	// some CI log viewers.
	t.Run("dump uses newlines rather than wire line endings", func(t *testing.T) {
		r := run(t, nil, "-s", "--assert-ok", url("/500"))
		assertExit(t, r, exitRequestFail)

		// The request half is still serialized by http.Request.Write and keeps
		// its CRLFs; #19 replaces it. Only the response half is checked here.
		_, dump, found := strings.Cut(r.Output(), "HTTP/1.1 500")
		if !found {
			t.Fatalf("no response status line in the dump\n%s", r.Output())
		}
		if strings.Contains(dump, "\r") {
			t.Errorf("response dump contains a carriage return: %q", dump)
		}
	})
}
