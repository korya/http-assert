package main

import (
	"io"
	"strings"
	"testing"
)

// The parsers below slice strings at offsets derived from strings.Index. Reading
// them suggests every index is guarded; fuzzing is what turns that reading into
// evidence. None of them should panic on any input, so each target simply calls
// the function -- an out-of-range slice or nil dereference fails the test by
// crashing it.
//
// Seed corpora run as ordinary subtests under `go test`, so these guard CI for
// free. To search for new inputs:
//
//	go test -run '^$' -fuzz FuzzParseHostMappings -fuzztime 60s

func FuzzParseHeaderLine(f *testing.F) {
	for _, s := range []string{
		"", ":", "a:b", "   a   :   b   ", "Content-Type",
		"Content-Type:", ":value", "a:b:c", "héllo: wörld", strings.Repeat(":", 100),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, l string) {
		name, value := parseHeaderLine(l)
		// A name is only empty when the input had nothing before the colon.
		if name == "" && value != "" && !strings.HasPrefix(strings.TrimSpace(l), ":") {
			t.Errorf("parseHeaderLine(%q) produced a value %q with no name", l, value)
		}
	})
}

func FuzzParseHostMappings(f *testing.F) {
	for _, s := range []string{
		"", "=", "a:1=b", "a:1=b:2", "a=b", "a:zz=b", ":::=====",
		"a:1=b c:2=d", strings.Repeat("=", 100), strings.Repeat("a:1=b ", 50),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, v string) {
		res, err := parseHostMappings(strings.Fields(v))
		// The parser abandons its accumulator on failure.
		if err != nil && res != nil {
			t.Errorf("parseHostMappings(%q) returned %d mappings alongside an error", v, len(res))
		}
		// Every accepted mapping must survive its own accessors.
		for _, m := range res {
			_ = m.Matches(v)
			_ = m.DstHost()
		}
	})
}

func FuzzHostMapping(f *testing.F) {
	for _, pair := range [][2]string{
		{"", ""}, {"a:1", "b:2"}, {"*:80", "b"}, {"*", "b"},
		{":", ":"}, {"a", ""}, {strings.Repeat(":", 50), strings.Repeat(":", 50)},
	} {
		f.Add(pair[0], pair[1])
	}

	f.Fuzz(func(t *testing.T, src, dst string) {
		m := hostMapping{Src: src, Dst: dst}
		_ = m.Matches(dst)
		_ = m.Matches(src)

		// A destination that already carries a port is returned unchanged.
		if got := m.DstHost(); strings.Contains(dst, ":") && got != dst {
			t.Errorf("hostMapping{%q, %q}.DstHost() = %q, want the destination unchanged", src, dst, got)
		}
	})
}

func FuzzPrintPayload(f *testing.F) {
	for _, seed := range []struct {
		Body []byte
		Max  int
	}{
		{nil, 0}, {[]byte("plain"), 100}, {[]byte("plain"), 2},
		{[]byte{0x00, 0xff, 0x07}, 100}, {[]byte("héllo"), 3}, {[]byte("x"), -1},
	} {
		f.Add(seed.Body, seed.Max)
	}

	f.Fuzz(func(t *testing.T, body []byte, maxSize int) {
		cropped := printPayload(io.Discard, body, maxSize)
		if cropped < 0 {
			t.Errorf("printPayload(%d bytes, max %d) reported %d cropped", len(body), maxSize, cropped)
		}
		if cropped > len(body) {
			t.Errorf("printPayload reported %d cropped from a %d-byte body", cropped, len(body))
		}
	})
}

func FuzzParseLogLevel(f *testing.F) {
	for _, s := range []string{"", "debug", "info", "warn", "error", "DEBUG", "  info  "} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		lvl, ok := parseLogLevel(s)
		if !ok && lvl != 0 {
			t.Errorf("parseLogLevel(%q) rejected the value but returned level %d", s, lvl)
		}
	})
}
