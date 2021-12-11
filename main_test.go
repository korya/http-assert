package main

import (
	"fmt"
	"testing"

	"github.com/onsi/gomega"
)

func Test_parseHostMappings(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Parallel()

	testCases := []struct {
		CaseName string
		Input    []string
		Output   []hostMapping
		Error    string
	}{
		{CaseName: "Null"},
		{CaseName: "Empty", Input: []string{}},
		// Simple
		{
			CaseName: "Simple",
			Input:    []string{"a:11=bbb:2222"},
			Output:   []hostMapping{{Src: "a:11", Dst: "bbb:2222"}},
		},
		{
			CaseName: "Simple: no dst port",
			Input:    []string{"a:11=bbb"},
			Output:   []hostMapping{{Src: "a:11", Dst: "bbb"}},
		},
		{
			CaseName: "Simple: no separators",
			Input:    []string{"a"},
			Error:    `value "a" has no separator, =`,
		},
		{
			CaseName: "Simple: no ports",
			Input:    []string{"a=bbb"},
			Error:    `value "a=bbb" has no src port "a"`,
		},
		{
			CaseName: "Simple: invalid src port",
			Input:    []string{"a:zz=bbb"},
			Error:    `value "a:zz=bbb" has invalid src port "zz"`,
		},
		{
			CaseName: "Simple: invalid dst port",
			Input:    []string{"a:11=bbb:zzzzz"},
			Error:    `value "a:11=bbb:zzzzz" has invalid dst port "zzzzz"`,
		},
		// Multple
		{
			CaseName: "Multiple 1",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:yyyy",
				"a:zz=bbb:yyyy",
				"a:zz=bbb",
				"a=bbb:yyyy",
				"a=bbb:2222",
				"a=bbb",
				"zzz",
			},
			Error: `value "a:11=bbb:yyyy" has invalid dst port "yyyy"`,
		},
		{
			CaseName: "Multiple 2",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"a:zz=bbb:yyyy",
				"a:zz=bbb",
				"a=bbb:yyyy",
				"a=bbb:2222",
				"a=bbb",
				"zzz",
			},
			Error: `value "a:zz=bbb:yyyy" has invalid src port "zz"`,
		},
		{
			CaseName: "Multiple 3",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"example.com:443=127.0.0.1",
				"a:zz=bbb",
				"a=bbb:yyyy",
				"a=bbb:2222",
				"a=bbb",
				"zzz",
			},
			Error: `value "a:zz=bbb" has invalid src port "zz"`,
		},
		{
			CaseName: "Multiple 4",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"example.com:443=127.0.0.1",
				"example.com:443=127.0.0.1:1443",
				"a=bbb:yyyy",
				"a=bbb:2222",
				"a=bbb",
				"zzz",
			},
			Error: `value "a=bbb:yyyy" has no src port "a"`,
		},
		{
			CaseName: "Multiple 5",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"example.com:443=127.0.0.1",
				"example.com:443=127.0.0.1:1443",
				"example.com:80=example.ca",
				"a=bbb:2222",
				"a=bbb",
				"zzz",
			},
			Error: `value "a=bbb:2222" has no src port "a"`,
		},
		{
			CaseName: "Multiple 6",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"example.com:443=127.0.0.1",
				"example.com:443=127.0.0.1:1443",
				"example.com:80=example.ca",
				"example.com:80=example.ca:1080",
				"a=bbb",
				"zzz",
			},
			Error: `value "a=bbb" has no src port "a"`,
		},
		{
			CaseName: "Multiple 7",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"example.com:443=127.0.0.1",
				"example.com:443=127.0.0.1:1443",
				"example.com:80=example.ca",
				"example.com:80=example.ca:1080",
				"zzz",
			},
			Error: `value "zzz" has no separator, =`,
		},
		{
			CaseName: "Multiple OK",
			Input: []string{
				"a:11=bbb:2222",
				"a:11=bbb",
				"a:11=bbb:333",
				"example.com:443=127.0.0.1",
				"example.com:443=127.0.0.1:1443",
				"example.com:80=example.ca",
				"example.com:80=example.ca:1080",
			},
			Output: []hostMapping{
				{Src: "a:11", Dst: "bbb:2222"},
				{Src: "a:11", Dst: "bbb"},
				{Src: "a:11", Dst: "bbb:333"},
				{Src: "example.com:443", Dst: "127.0.0.1"},
				{Src: "example.com:443", Dst: "127.0.0.1:1443"},
				{Src: "example.com:80", Dst: "example.ca"},
				{Src: "example.com:80", Dst: "example.ca:1080"},
			},
		},
	}

	for _, tc := range testCases {
		res, err := parseHostMappings(tc.Input)
		if tc.Error == "" {
			g.Expect(res).To(gomega.Equal(tc.Output), tc.CaseName)
		} else {
			g.Expect(err).To(gomega.MatchError(tc.Error), tc.CaseName)
		}
	}
}

func TestHostMapping_Matches(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Parallel()

	testCases := []struct {
		SrcHost string
		Input   string
		Output  bool
	}{
		{"", "", false},
		{"src", "", false},
		{"src", "example.com", false},
		{"", "example.com", false},
		{"src", "src", true},
		{"example.com", "example.com", true},
		{"example.com:80", "example.com", false},
		{"example.com:80", "example.com:99", false},
		{"example.com", "example.com:99", false},
		{"example.com:99", "example.com:99", true},
		{"*", "", true},
		{"*", "example.com", true},
		{"*", "example.com:99", true},
		{"*:12", "", false},
		{"*:12", "example.com", false},
		{"*:12", "example.com:99", false},
		{"*:12", "example.com:12", true},
	}

	for _, tc := range testCases {
		caseName := fmt.Sprintf("%q = %q", tc.SrcHost, tc.Input)
		m := hostMapping{Src: tc.SrcHost}
		g.Expect(m.Matches(tc.Input)).To(gomega.Equal(tc.Output), caseName)
	}
}

func TestHostMapping_DstHost(t *testing.T) {
	g := gomega.NewWithT(t)

	t.Parallel()

	testCases := []struct {
		SrcHost string
		DstHost string
		Output  string
	}{
		{"", "", ""},
		{"src", "", ""},
		{"src", "example.com", "example.com"},
		{"", "example.com", "example.com"},
		{"src", "src", "src"},
		{"example.com", "example.ca", "example.ca"},
		{"example.com:80", "example.ca", "example.ca:80"},
		{"example.com:80", "example.ca:99", "example.ca:99"},
		{"example.com", "example.ca:99", "example.ca:99"},
		{"example.com:99", "example.ca:99", "example.ca:99"},
	}

	for _, tc := range testCases {
		caseName := fmt.Sprintf("src=%q dst=%q", tc.SrcHost, tc.DstHost)
		m := hostMapping{Src: tc.SrcHost, Dst: tc.DstHost}
		g.Expect(m.DstHost()).To(gomega.Equal(tc.Output), caseName)
	}
}
