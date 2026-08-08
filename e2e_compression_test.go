package main_test

import "testing"

// Compression is the one transformation that sits between the bytes on the wire
// and what an assertion reads, so what the CLI does with it is worth pinning
// precisely: which encodings it removes, what it does with one it cannot,
// which assertions that affects, and what the headers say afterwards.
//
// Before #27 was fixed, whether --assert-body saw the payload or a compressed
// blob depended on a caller-set Accept-Encoding, a Range header, and the
// server's choice of encoding -- none of which have anything to do with a body.

// TestE2ECompressionDecodesTheBody covers the case in the report and the ways
// in that it did not mention.
func TestE2ECompressionDecodesTheBody(t *testing.T) {
	// The reported invocation, verbatim.
	t.Run("a caller-set Accept-Encoding", func(t *testing.T) {
		r := run(t, nil, "-H", "Accept-Encoding: gzip",
			"--assert-body", `"status":"success"`, url("/gzip"))
		assertExit(t, r, exitOK)
	})

	// The transport used to decode only what it had asked for itself, so a
	// server compressing unbidden broke assertions with no flag involved.
	t.Run("a server that compresses unasked", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body", `"status":"success"`, url("/gzip-always")), exitOK)
	})

	// net/http never negotiates deflate, so it never decoded one either. This
	// needed no flag to go wrong.
	t.Run("deflate, raw", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body", `"status":"success"`, url("/deflate")), exitOK)
	})

	t.Run("deflate, zlib-wrapped", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-body", `"status":"success"`, url("/deflate-zlib")), exitOK)
	})

	// A Range header also stopped the transport asking for gzip, and so also
	// stopped it decoding one.
	t.Run("a Range header", func(t *testing.T) {
		r := run(t, nil, "-H", "Range: bytes=0-999",
			"--assert-body", `"status":"success"`, url("/gzip-always"))
		assertExit(t, r, exitOK)
	})

	// All three body assertions, not just the one in the report.
	t.Run("every body assertion", func(t *testing.T) {
		for _, args := range [][]string{
			{"--assert-body", `"status":"success"`},
			{"--assert-body-eq", `{"status":"success"}`},
		} {
			t.Run(args[0], func(t *testing.T) {
				full := append(append([]string{"-H", "Accept-Encoding: gzip"}, args...), url("/gzip"))
				assertExit(t, run(t, nil, full...), exitOK)
			})
		}

		// --assert-body-empty needs a body that really is empty, and a 204 that
		// carries the encoding header anyway is the awkward case: there is
		// nothing to decode, and an empty gzip stream is an error rather than
		// an empty payload.
		t.Run("--assert-body-empty", func(t *testing.T) {
			assertExit(t, run(t, nil, "--assert-body-empty", url("/empty-gzip")), exitOK)
		})
	})
}

// TestE2ECompressionNoFalsePass is the dangerous half of #27. A confusing
// failure wastes an afternoon; a pattern loose enough to match compressed noise
// produces a green run that checked nothing, which is the one outcome this
// program exists to refuse.
func TestE2ECompressionNoFalsePass(t *testing.T) {
	// Passes either way -- the point is what it matched. Pair it with a
	// pattern that can only match the decoded payload.
	r := run(t, nil, "-H", "Accept-Encoding: gzip",
		"--assert-body", ".",
		"--assert-body-eq", `{"status":"success"}`,
		url("/gzip"))
	assertExit(t, r, exitOK)

	// And the inverse: a pattern that matches only the compressed form must
	// now fail, because the compressed form is not what gets asserted on.
	bad := run(t, nil, "-H", "Accept-Encoding: gzip",
		"--assert-body", `\x1f\x8b`, url("/gzip"))
	assertExit(t, bad, exitRequestFail)
}

// TestE2ECompressionHeadersSurvive pins the half of the fix that is not about
// the body. net/http deletes Content-Encoding and Content-Length when it
// decodes, so a response that was compressed became indistinguishable from one
// that never was -- and telling those apart is the reason to set
// Accept-Encoding by hand in the first place.
func TestE2ECompressionHeadersSurvive(t *testing.T) {
	t.Run("the encoding is still assertable alongside the decoded body", func(t *testing.T) {
		r := run(t, nil, "-H", "Accept-Encoding: gzip",
			"--assert-header-eq", "Content-Encoding: gzip",
			"--assert-body-eq", `{"status":"success"}`,
			url("/gzip"))
		assertExit(t, r, exitOK)
	})

	// The CLI no longer advertises gzip on its own, so a server that only
	// compresses on request answers in plain -- and says nothing about encoding.
	t.Run("nothing is advertised unless asked", func(t *testing.T) {
		r := run(t, nil, "--assert-body", `"headers"`,
			"--assert-body-eq", "never-matches", url("/echo"))
		assertExit(t, r, exitRequestFail)
		assertNotContains(t, r, "Accept-Encoding")
	})
}

// TestE2ECompressionUndecodable covers an encoding with no decoder here.
//
// The response still has a status and headers worth asserting on, so only the
// body assertions refuse. Failing the whole run would be simpler and would
// break a status check against any CDN serving brotli.
func TestE2ECompressionUndecodable(t *testing.T) {
	t.Run("a status check is unaffected", func(t *testing.T) {
		assertExit(t, run(t, nil, "--assert-ok", url("/brotli")), exitOK)
	})

	t.Run("a header check is unaffected", func(t *testing.T) {
		r := run(t, nil, "--assert-header-eq", "Content-Encoding: br", url("/brotli"))
		assertExit(t, r, exitOK)
	})

	t.Run("a body check refuses, and names the encoding", func(t *testing.T) {
		r := run(t, nil, "--assert-body", `"status":"success"`, url("/brotli"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "body: response is br-encoded and was not decoded")
		assertContains(t, r, "no decoder for \"br\"")
		// The old failure was a bare hex dump with nothing explaining it.
		assertContains(t, r, "<< Payload is br-encoded and was not decoded >>")
	})

	// A header claiming an encoding the body does not have. Treating those
	// bytes as plain would be its own silent corruption.
	t.Run("a body that does not match its stated encoding", func(t *testing.T) {
		r := run(t, nil, "--assert-body-eq", `{"status":"success"}`, url("/gzip-corrupt"))
		assertExit(t, r, exitRequestFail)
		assertContains(t, r, "body: response is gzip-encoded and was not decoded")
	})
}

// TestE2ECompressionFailureDumpExplainsItself: the headers in the dump still
// say the body arrived compressed, so plain text underneath needs a word of
// explanation -- otherwise the dump reads as a contradiction.
func TestE2ECompressionFailureDumpExplainsItself(t *testing.T) {
	r := run(t, nil, "-H", "Accept-Encoding: gzip",
		"--assert-body-eq", "never-matches", url("/gzip"))
	assertExit(t, r, exitRequestFail)

	assertContains(t, r, "Content-Encoding: gzip")
	assertContains(t, r, "<< Payload decoded from gzip >>")
	assertContains(t, r, `{"status":"success"}`)

	// And it stays out of the way when nothing was encoded.
	plain := run(t, nil, "--assert-body-eq", "never-matches", url("/ok"))
	assertNotContains(t, plain, "Payload decoded from")
}
