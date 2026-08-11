package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"net/http"
	"testing"
)

const payload = `{"status":"success"}`

func gzipped(t *testing.T, s string) []byte {
	t.Helper()

	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatalf("cannot gzip: %s", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cannot close the gzip writer: %s", err)
	}

	return b.Bytes()
}

func deflatedRaw(t *testing.T, s string) []byte {
	t.Helper()

	var b bytes.Buffer
	zw, err := flate.NewWriter(&b, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("cannot build the flate writer: %s", err)
	}
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatalf("cannot deflate: %s", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cannot close the flate writer: %s", err)
	}

	return b.Bytes()
}

func deflatedZlib(t *testing.T, s string) []byte {
	t.Helper()

	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatalf("cannot zlib: %s", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cannot close the zlib writer: %s", err)
	}

	return b.Bytes()
}

// encoded builds the shape decodeBody operates on: a body and the header that
// claims how it was encoded. It reuses render_test.go's response helper so both
// files describe an httpResponse the same way.
func encoded(enc string, body []byte) *httpResponse {
	h := http.Header{}
	if enc != "" {
		h.Set("Content-Encoding", enc)
	}

	r := response("200 OK", h, "")
	r.BodyBytes = body

	return &r
}

// Test_decodeBody is the whole of #27 in one table: what the assertions end up
// reading, for every way a body can arrive.
func Test_decodeBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name string
		Enc  string
		Body []byte
		// Want is the payload the assertions should see. Only checked when
		// WantErr is false.
		Want string
		// WantErr means the body could not be decoded, so the body assertions
		// must refuse rather than match against what is there.
		WantErr bool
		// WantEncoding is what Content-Encoding is recorded as, verbatim.
		WantEncoding string
	}{
		{
			Name: "no encoding at all",
			Body: []byte(payload),
			Want: payload,
		},
		{
			Name:         "identity is not an encoding to remove",
			Enc:          "identity",
			Body:         []byte(payload),
			Want:         payload,
			WantEncoding: "identity",
		},
		{
			Name:         "gzip",
			Enc:          "gzip",
			Body:         gzipped(t, payload),
			Want:         payload,
			WantEncoding: "gzip",
		},
		{
			// Content coding names are case-insensitive per RFC 9110, and a
			// case-sensitive lookup would refuse a body it can decode.
			Name:         "GZIP, in the case the server chose",
			Enc:          "GZIP",
			Body:         gzipped(t, payload),
			Want:         payload,
			WantEncoding: "GZIP",
		},
		{
			Name:         "gzip with surrounding whitespace",
			Enc:          " gzip ",
			Body:         gzipped(t, payload),
			Want:         payload,
			WantEncoding: "gzip",
		},
		{
			// The two formats that share the deflate name. Neither reader
			// accepts the other's input, so trying both is safe.
			Name:         "deflate, raw",
			Enc:          "deflate",
			Body:         deflatedRaw(t, payload),
			Want:         payload,
			WantEncoding: "deflate",
		},
		{
			Name:         "deflate, zlib-wrapped",
			Enc:          "deflate",
			Body:         deflatedZlib(t, payload),
			Want:         payload,
			WantEncoding: "deflate",
		},
		{
			Name:         "an encoding with no decoder here",
			Enc:          "compress",
			Body:         []byte("payload"),
			WantErr:      true,
			WantEncoding: "compress",
		},
		{
			// The header claims gzip and the bytes are not. Silently asserting
			// against them is the bug; so is silently treating them as plain.
			Name:         "a body that does not match its stated encoding",
			Enc:          "gzip",
			Body:         []byte(payload),
			WantErr:      true,
			WantEncoding: "gzip",
		},
		{
			Name:         "a truncated gzip stream",
			Enc:          "gzip",
			Body:         gzipped(t, payload)[:10],
			WantErr:      true,
			WantEncoding: "gzip",
		},
		{
			// A 204 that carries the header anyway. There is nothing to decode,
			// and an empty gzip stream is an error rather than an empty
			// payload -- so --assert-body-empty must still pass.
			Name:         "an empty body with an encoding header",
			Enc:          "gzip",
			Body:         nil,
			Want:         "",
			WantEncoding: "gzip",
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			res := encoded(tc.Enc, tc.Body)
			res.decodeBody()

			if got, want := res.Encoding, tc.WantEncoding; got != want {
				t.Errorf("Encoding = %q, want %q", got, want)
			}

			if tc.WantErr {
				if res.DecodeErr == nil {
					t.Fatalf("DecodeErr = nil, want an error; body reads %q", string(res.BodyBytes))
				}
				// The bytes are left exactly as they arrived, so the failure
				// dump can still show what was really there.
				if !bytes.Equal(res.BodyBytes, tc.Body) {
					t.Errorf("BodyBytes was modified despite the decode failing")
				}
				return
			}

			if res.DecodeErr != nil {
				t.Fatalf("DecodeErr = %s, want nil", res.DecodeErr)
			}
			if got := string(res.BodyBytes); got != tc.Want {
				t.Errorf("BodyBytes = %q, want %q", got, tc.Want)
			}
		})
	}
}

// Test_decodeBody_leavesHeadersAlone pins the half of the fix that is not about
// the body. net/http deletes Content-Encoding and Content-Length when it
// decodes, which makes a compressed response indistinguishable from one that
// never was -- and telling those apart is the reason to set Accept-Encoding by
// hand in the first place.
func Test_decodeBody_leavesHeadersAlone(t *testing.T) {
	t.Parallel()

	res := encoded("gzip", gzipped(t, payload))
	res.Header.Set("Content-Length", "44")

	res.decodeBody()

	if got, want := res.Header.Get("Content-Encoding"), "gzip"; got != want {
		t.Errorf("Content-Encoding = %q, want %q -- the header must survive decoding", got, want)
	}
	if got, want := res.Header.Get("Content-Length"), "44"; got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
}

// Test_bodyOf covers the guard every body assertion goes through.
func Test_bodyOf(t *testing.T) {
	t.Parallel()

	t.Run("a decoded body is returned as-is", func(t *testing.T) {
		res := encoded("gzip", gzipped(t, payload))
		res.decodeBody()

		body, err := bodyOf(res)
		checkErr(t, "bodyOf", err, "")
		if got := string(body); got != payload {
			t.Errorf("body = %q, want %q", got, payload)
		}
	})

	t.Run("an undecoded body is refused, and says why", func(t *testing.T) {
		res := encoded("compress", []byte("payload"))
		res.decodeBody()

		_, err := bodyOf(res)
		checkErrMatch(t, "bodyOf", err, `^body: response is compress-encoded and was not decoded: `)
	})
}

// Test_bodyAssertionsRefuseAnEncodedBody: the guard has to be on all three, not
// on the one that was reported. A missed assertion is a silent false pass.
func Test_bodyAssertionsRefuseAnEncodedBody(t *testing.T) {
	t.Parallel()

	// A pattern loose enough to match almost any bytes. Before the guard this
	// passed against compressed noise, which is the dangerous half of #27: not
	// a confusing failure, but a green run that checked nothing.
	match, err := AssertBodyMatch(".")
	if err != nil {
		t.Fatalf("cannot build the assertion: %s", err)
	}

	assertions := map[string]Assertion{
		"AssertBodyMatch": match,
		"AssertBodyEqual": AssertBodyEqual(payload),
		"AssertBodyEmpty": AssertBodyEmpty(),
	}

	for name, a := range assertions {
		t.Run(name, func(t *testing.T) {
			res := encoded("compress", []byte("payload"))
			res.decodeBody()

			checkErrMatch(t, name, check(a, res), `^body: response is compress-encoded and was not decoded: `)
		})
	}
}
