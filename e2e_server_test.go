package main_test

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

	// Two values under one header name, for the multi-value matching path.
	mux.HandleFunc("/multi", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte("ok"), http.Header{"Set-Cookie": {"a=1", "b=2"}})
	})

	// Non-printable bytes, which routes the failure dump through the hex dumper.
	mux.HandleFunc("/binary", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, []byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0x07, 0x08}, nil)
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

	// Compresses only when the client asked, so the suite can exercise both the
	// transparent-decompression path and the caller-set-the-header path.
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		raw := []byte(`{"status":"success"}`)
		if !acceptsGzip(r) {
			write(w, http.StatusOK, raw, nil)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		zw := gzip.NewWriter(w)
		_, _ = zw.Write(raw)
		_ = zw.Close()
	})

	// Reflects the request so tests can observe -X, -H and -d taking effect.
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
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
	})

	return mux
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
