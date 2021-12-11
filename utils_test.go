package main

import (
	"strings"
	"testing"

	"github.com/onsi/gomega"
)

func Test_parseHeaderLine(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Parallel()

	testCases := []struct {
		Input    string
		ExpName  string
		ExpValue string
	}{
		{"", "", ""},
		{"a", "A", ""},
		{"a:b", "A", "b"},
		{" a   :   b     ", "A", "b"},
		// Normalize name
		{"Content-Type", "Content-Type", ""},
		{"Content-Type:", "Content-Type", ""},
		{"Content-Type: ", "Content-Type", ""},
		{"cONTENT-tYPE", "Content-Type", ""},
		{"   content-type   ", "Content-Type", ""},
		{"   content-type      : ", "Content-Type", ""},
		// Others
		{"Content-Length: one-two-three", "Content-Length", "one-two-three"},
		{"Content-length: oNe-tWo-tHrEe  ", "Content-Length", "oNe-tWo-tHrEe"},
		{"Content-LENGTH:one ", "Content-Length", "one"},
		{"cONTENT-lENGTH: ONE-two-thREE     ", "Content-Length", "ONE-two-thREE"},
		{"   content-length   :    one - tWo - thRee     ", "Content-Length", "one - tWo - thRee"},
	}

	type h struct{ Name, Value string }

	for _, tc := range testCases {
		name, value := parseHeaderLine(tc.Input)
		g.Expect(h{name, value}).To(gomega.Equal(h{tc.ExpName, tc.ExpValue}), tc.Input)
	}
}

func Test_printPayload(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Parallel()

	testCases := []struct {
		CaseName     string
		Input        []byte
		MaxSize      int
		Output       string
		CroppedBytes int
	}{
		{CaseName: "empty"},
		{CaseName: "empty, 10", MaxSize: 10},
		// Single line
		{
			CaseName: "single line, ASCII, 100",
			Input:    []byte("single line"),
			MaxSize:  100,
			Output:   "single line",
		},
		{
			CaseName:     "single line, ASCII, 10",
			Input:        []byte("single line"),
			MaxSize:      10,
			Output:       "single lin",
			CroppedBytes: 1,
		},
		{
			CaseName:     "single line, ASCII, 1",
			Input:        []byte("single line"),
			MaxSize:      1,
			Output:       "s",
			CroppedBytes: 10,
		},
		{
			CaseName:     "single line, ASCII, 0",
			Input:        []byte("single line"),
			MaxSize:      0,
			Output:       "",
			CroppedBytes: 11,
		},
		{
			CaseName: "single line, BIN, 100",
			Input:    []byte("\x01single\x00line"),
			MaxSize:  100,
			Output:   "00000000  01 73 69 6e 67 6c 65 00  6c 69 6e 65              |.single.line|\n",
		},
		{
			CaseName:     "single line, BIN, 10",
			Input:        []byte("\x01single\x00line"),
			MaxSize:      10,
			Output:       "00000000  01 73 69 6e 67 6c 65 00  6c 69                    |.single.li|\n",
			CroppedBytes: 2,
		},
		{
			CaseName:     "single line, BIN, 1",
			Input:        []byte("\x01single\x00line"),
			MaxSize:      1,
			Output:       "00000000  01                                                |.|\n",
			CroppedBytes: 11,
		},
		{
			CaseName:     "single line, BIN, 0",
			Input:        []byte("\x01single\x00line"),
			MaxSize:      0,
			Output:       "",
			CroppedBytes: 12,
		},
	}

	for _, tc := range testCases {
		var b strings.Builder
		n := printPayload(&b, tc.Input, tc.MaxSize)
		g.Expect(b.String()).To(gomega.Equal(tc.Output), tc.CaseName)
		g.Expect(n).To(gomega.Equal(tc.CroppedBytes), tc.CaseName)
	}
}
