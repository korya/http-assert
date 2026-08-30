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
	// A negative limit would slice to a negative bound and panic. The only
	// caller passes a constant, so this is unreachable today -- but the
	// function is exported to the rest of the package and should be total.
	if maxSize < 0 {
		maxSize = 0
	}

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

// isPrintable reports whether bs should be shown as text rather than dumped as
// hex.
//
// Whitespace counts as text. unicode.IsPrint answers false for '\n', '\t' and
// '\r', so testing it alone sent every multi-line body -- pretty-printed JSON,
// HTML, a log excerpt, anything with a line break in the first 256 bytes -- to
// the hex dumper, which is the least readable way to show text a human was
// about to read.
//
// Known gap: bytes that are not valid UTF-8 decode to U+FFFD, which is itself
// printable, so a body of high bytes still reads as text. Cropping happens
// before this check and can split a multi-byte rune, so the obvious utf8.Valid
// guard would misfile legitimate text; tracked separately.
func isPrintable(bs []byte) bool {
	nonPrintableIdx := bytes.IndexFunc(bs, func(r rune) bool {
		return !unicode.IsPrint(r) && !unicode.IsSpace(r)
	})
	return nonPrintableIdx < 0
}
