package main

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"unicode"
)

func parseHeaderLine(l string) (name, value string) {
	if idx := strings.Index(l, ":"); idx < 0 {
		name = l
	} else {
		name = l[:idx]
		value = l[idx+1:]
	}
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	return
}

func printPayload(w io.Writer, bs []byte, maxSize int) (croppedBytes int) {
	if n := len(bs); n > maxSize {
		bs = bs[:maxSize]
		croppedBytes = n - maxSize
	}
	if isPrintable(bs) {
		_, _ = w.Write(bs)
	} else {
		d := hex.Dumper(w)
		defer func() { _ = d.Close() }()
		_, _ = d.Write(bs)
	}
	return
}

func isPrintable(bs []byte) bool {
	nonPrintableIdx := bytes.IndexFunc(bs, func(r rune) bool {
		return !unicode.IsPrint(r)
	})
	return nonPrintableIdx < 0
}
