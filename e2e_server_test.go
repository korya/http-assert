package main_test

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The e2e suite talks to two real servers started once per test run: a plain
// HTTP one and a TLS one with a self-signed certificate (which is what makes
// --insecure observable). Subprocesses reach them over the loopback interface.
var (
	srv    *httptest.Server
	tlsSrv *httptest.Server
)

func TestMain(m *testing.M) {
	srv = httptest.NewServer(testHandler())
	tlsSrv = httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			write(w, http.StatusOK, []byte("secure-ok"), nil)
		}))
	// Several tests deliberately connect without --insecure, so the server logs
	// a rejected handshake every time. That is the expected result, not a
	// failure; keep it out of the test output.
	tlsSrv.Config.ErrorLog = log.New(io.Discard, "", 0)

	code := m.Run()

	srv.Close()
	tlsSrv.Close()
	os.Exit(code)
}

// url builds an absolute URL against the plain HTTP test server.
func url(path string) string { return srv.URL + path }

// hostPort is the test server's authority, for --maphost destinations.
func hostPort() string { return srv.Listener.Addr().String() }

func testHandler() http.Handler {
	mux := http.NewServeMux()

	// A well-formed JSON response carrying the headers the assertion tests match on.
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte(`{"status":"success","users":[]}`), http.Header{
			"Content-Type":  {"application/json"},
			"X-Api-Version": {"v1"},
			"Cache-Control": {"max-age=3600"},
		})
	})

	mux.HandleFunc("/created", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusCreated, []byte("created"), nil)
	})

	// 204 carries no body at all, which is what --assert-body-empty needs.
	mux.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/500", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusInternalServerError, []byte("boom"), nil)
	})

	mux.HandleFunc("/redirect", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusFound, nil, http.Header{
			"Location": {"https://new-domain.com/path"},
		})
	})

	// A relative Location, which the CLI compares verbatim rather than resolving.
	mux.HandleFunc("/redirect-rel", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusMovedPermanently, nil, http.Header{"Location": {"/target"}})
	})

	// The endpoints below exist for --location. Note that /redirect above
	// points at a real external host, so it must never be used with -L: the
	// suite would leave the loopback interface and reach the internet.

	// A redirect chain of a chosen length: /hop?n=3 -> /hop?n=2 -> ... ->
	// /hop?n=0, which is the destination.
	mux.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil || n <= 0 {
			write(w, http.StatusOK, []byte("arrived"), nil)
			return
		}
		write(w, http.StatusFound, nil, http.Header{
			"Location": {fmt.Sprintf("/hop?n=%d", n-1)},
		})
	})

	// Redirects to itself forever. Only the hop limit can end it, which is what
	// makes it a test of the limit rather than of patience.
	mux.HandleFunc("/redirect-loop", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusFound, nil, http.Header{"Location": {"/redirect-loop"}})
	})

	// One hop onto /ok, so the assertions that already match that payload can
	// be pointed at the far side of a redirect.
	mux.HandleFunc("/redirect-local", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusFound, nil, http.Header{"Location": {"/ok"}})
	})

	// 308 preserves the method and body across the hop; the 302 above does not.
	mux.HandleFunc("/redirect-308", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusPermanentRedirect, nil, http.Header{"Location": {"/echo"}})
	})

	// The endpoints below exist for --retry: each fails a fixed number of times
	// and then starts succeeding, which is the shape of a service coming up.
	//
	// Every one of them is keyed by an `id` the caller invents, because the
	// count has to survive across subprocesses and must not be shared between
	// two tests that happen to run together.

	// /flaky?id=X&fail=N answers 503 to the first N requests carrying id X and
	// 200 "healthy" from then on. A 503 fails --assert-ok, so this exercises
	// retrying an assertion rather than a transport error.
	mux.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		if hits(r) <= failCount(r) {
			write(w, http.StatusServiceUnavailable, []byte("starting"), nil)
			return
		}
		write(w, http.StatusOK, []byte("healthy"), nil)
	})

	// /flaky-echo?id=X&fail=N is /flaky with /echo's payload once it recovers,
	// so a test can check what the *last* attempt actually sent.
	mux.HandleFunc("/flaky-echo", func(w http.ResponseWriter, r *http.Request) {
		if hits(r) <= failCount(r) {
			write(w, http.StatusServiceUnavailable, []byte("starting"), nil)
			return
		}
		echo(w, r)
	})

	// /flaky-hangup?id=X&fail=N drops the connection instead of answering, so
	// the CLI sees a transport error rather than a response it can assert on.
	mux.HandleFunc("/flaky-hangup", func(w http.ResponseWriter, r *http.Request) {
		if hits(r) > failCount(r) {
			write(w, http.StatusOK, []byte("healthy"), nil)
			return
		}
		// Hijacking and closing without writing anything is the only way to
		// produce a connection failure from inside a handler.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			write(w, http.StatusInternalServerError, []byte(err.Error()), nil)
			return
		}
		_ = conn.Close()
	})

	// A document with enough shape for jq to work on: an array to iterate, a
	// nested object, and the three scalar types.
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte(`{"status":"success","count":2,"active":true,`+
			`"meta":{"version":"v1"},`+
			`"users":[{"id":1,"name":"alice","active":true},`+
			`{"id":2,"name":"bob","active":true}]}`),
			http.Header{"Content-Type": {"application/json"}})
	})

	// Valid JSON served gzipped, so a jq assertion can be shown to run against
	// the decoded payload rather than the bytes on the wire.
	mux.HandleFunc("/json-gzip", func(w http.ResponseWriter, _ *http.Request) {
		writeGzip(w, []byte(`{"status":"success","count":2}`))
	})

	// Two values under one header name, for the multi-value matching path.
	mux.HandleFunc("/multi", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte("ok"), http.Header{"Set-Cookie": {"a=1", "b=2"}})
	})

	// Non-printable bytes, which routes the failure dump through the hex dumper.
	mux.HandleFunc("/binary", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0x07, 0x08}, nil)
	})

	// A multi-line text body: the shape most real responses have, and the one
	// that used to reach the hex dumper because '\n' is not unicode.IsPrint.
	mux.HandleFunc("/multiline", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte("{\n  \"status\": \"success\"\n}"), nil)
	})

	// Flushes before the response buffer fills, which forces chunked transfer
	// and leaves Content-Length unset. The failure dump must show the body
	// without the framing that carried it (#18).
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("first chunk "))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("second chunk"))
	})

	// Larger than the 256-byte crop threshold used when printing payloads.
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		body := make([]byte, 5000)
		for i := range body {
			body[i] = 'X'
		}
		write(w, http.StatusOK, body, nil)
	})

	// Sleeps so --max-time has something to time out against. Kept short; the
	// timeout tests use 1s and this only has to outlast it.
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		d := 2 * time.Second
		if ms, err := strconv.Atoi(r.URL.Query().Get("ms")); err == nil {
			d = time.Duration(ms) * time.Millisecond
		}
		time.Sleep(d)
		write(w, http.StatusOK, []byte("slow"), nil)
	})

	// The endpoints below exist for #27. Compression is the one transformation
	// that sits between the bytes on the wire and what an assertion reads, so
	// each way of applying it gets an endpoint.

	// Compresses only when the client asked. Since the CLI no longer advertises
	// gzip on its own, this answers plain unless -H says otherwise.
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		raw := []byte(`{"status":"success"}`)
		if !acceptsGzip(r) {
			write(w, http.StatusOK, raw, nil)
			return
		}
		writeGzip(w, raw)
	})

	// Compresses whether or not the client asked, as a CDN with compression
	// forced on does. Nothing the caller passes can avoid the encoding here.
	mux.HandleFunc("/gzip-always", func(w http.ResponseWriter, _ *http.Request) {
		writeGzip(w, []byte(`{"status":"success"}`))
	})

	// deflate is two formats under one name: RFC 9110 says zlib, and plenty of
	// servers send raw. Both are served so the guessing can be tested.
	mux.HandleFunc("/deflate", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "deflate")
		w.WriteHeader(http.StatusOK)
		zw, _ := flate.NewWriter(w, flate.DefaultCompression)
		_, _ = zw.Write([]byte(`{"status":"success"}`))
		_ = zw.Close()
	})

	mux.HandleFunc("/deflate-zlib", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "deflate")
		w.WriteHeader(http.StatusOK)
		zw := zlib.NewWriter(w)
		_, _ = zw.Write([]byte(`{"status":"success"}`))
		_ = zw.Close()
	})

	// An encoding with no decoder here. Brotli is the realistic case; the bytes
	// are not real brotli because nothing in the suite could produce them, and
	// the CLI never gets far enough to care.
	mux.HandleFunc("/brotli", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte{0x1b, 0x13, 0x00, 0x00, 0xa4, 0xb0, 0xb2},
			http.Header{"Content-Encoding": {"br"}})
	})

	// Claims an encoding and then does not apply it, which is what a corrupt or
	// truncated stream looks like from here.
	mux.HandleFunc("/gzip-corrupt", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte(`{"status":"success"}`),
			http.Header{"Content-Encoding": {"gzip"}})
	})

	// 204 with an encoding header and no body at all. There is nothing to
	// decode, so --assert-body-empty must still pass.
	mux.HandleFunc("/empty-gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusNoContent)
	})

	// Reflects the request so tests can observe -X, -H and -d taking effect.
	mux.HandleFunc("/echo", echo)

	return mux
}

func echo(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, 0, 512)
	if r.Body != nil {
		buf := make([]byte, 512)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"method":  r.Method,
		"body":    string(body),
		"headers": r.Header,
		"host":    r.Host,
	})
	write(w, http.StatusOK, payload, http.Header{"Content-Type": {"application/json"}})
}

// flakyHits counts requests per `id`, so the flaky endpoints can fail a fixed
// number of times and then recover. Keyed rather than global because the CLI
// under test is a subprocess -- the counter cannot live in it -- and two tests
// sharing one counter would each see the other's attempts.
var flakyHits sync.Map // id -> *atomic.Int64

// hits returns how many requests this one is, counting from 1, for the id in
// the query string.
func hits(r *http.Request) int64 {
	v, _ := flakyHits.LoadOrStore(r.URL.Query().Get("id"), &atomic.Int64{})
	return v.(*atomic.Int64).Add(1)
}

// failCount is how many requests the caller asked to have fail before the
// endpoint recovers. Absent or unparseable means "fail every time".
func failCount(r *http.Request) int64 {
	n, err := strconv.ParseInt(r.URL.Query().Get("fail"), 10, 64)
	if err != nil {
		return math.MaxInt64
	}
	return n
}

func writeGzip(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Encoding", "gzip")
	w.WriteHeader(http.StatusOK)
	zw := gzip.NewWriter(w)
	_, _ = zw.Write(raw)
	_ = zw.Close()
}

func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		if v == "gzip" || v == "gzip, deflate" {
			return true
		}
	}
	return false
}

// write sends a complete response with an explicit Content-Length, so the
// framing the CLI sees is deterministic across Go versions.
func write(w http.ResponseWriter, code int, body []byte, hdrs http.Header) {
	for k, vs := range hdrs {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(code)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}
