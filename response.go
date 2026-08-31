package httpassert

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Response is the response assertions inspect. Response.Body has already been
// consumed and closed; BodyBytes contains the decoded payload when DecodeErr is
// nil. The original headers, including Content-Encoding and Content-Length,
// remain unchanged.
type Response struct {
	*http.Response
	// BodyBytes is the complete decoded response payload when DecodeErr is
	// nil. Client.Do reads it before evaluating any assertion.
	BodyBytes []byte
	// Encoding is the response's Content-Encoding value, with surrounding
	// whitespace removed. An empty value means no encoding was declared.
	Encoding string
	// DecodeErr explains why BodyBytes could not be decoded. When non-nil,
	// BodyBytes contains the encoded bytes exactly as received.
	DecodeErr error

	jsonBody   any
	jsonErr    error
	jsonParsed bool
}

func (r *Response) decodeJSON() (any, error) {
	if r.jsonParsed {
		return r.jsonBody, r.jsonErr
	}
	r.jsonParsed = true

	body, err := bodyOf(r)
	if err != nil {
		r.jsonErr = err
		return nil, r.jsonErr
	}

	if err := json.Unmarshal(body, &r.jsonBody); err != nil {
		r.jsonErr = &EvaluationError{
			Code:  EvaluationJSON,
			Kind:  KindBody,
			Cause: err,
		}
		return nil, r.jsonErr
	}

	return r.jsonBody, nil
}

var decoders = map[string]func([]byte) ([]byte, error){
	"gzip":    decodeGzip,
	"deflate": decodeDeflate,
	"br":      decodeBrotli,
	"zstd":    decodeZstd,
}

func supportedCodings() string {
	return strings.Join(slices.Sorted(maps.Keys(decoders)), ", ")
}

func (r *Response) decodeBody() {
	r.Encoding = strings.TrimSpace(r.Header.Get("Content-Encoding"))
	if len(r.BodyBytes) == 0 {
		return
	}

	switch enc := strings.ToLower(r.Encoding); enc {
	case "", "identity":
		return
	default:
		decode, ok := decoders[enc]
		if !ok {
			r.DecodeErr = fmt.Errorf("no decoder for %q; %s are supported", r.Encoding, supportedCodings())
			return
		}

		body, err := decode(r.BodyBytes)
		if err != nil {
			r.DecodeErr = err
			return
		}
		r.BodyBytes = body
	}
}

func decodeBrotli(body []byte) ([]byte, error) {
	return io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
}

func decodeZstd(body []byte) ([]byte, error) {
	// A response body is consumed synchronously before assertions run. One
	// decoder worker avoids starting a background decode pipeline and excess
	// per-response workers.
	reader, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func decodeGzip(body []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}

func decodeDeflate(body []byte) ([]byte, error) {
	if reader, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
		defer func() { _ = reader.Close() }()
		if out, err := io.ReadAll(reader); err == nil {
			return out, nil
		}
	}

	reader := flate.NewReader(bytes.NewReader(body))
	defer func() { _ = reader.Close() }()
	out, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("not valid zlib or raw DEFLATE: %w", err)
	}
	return out, nil
}
