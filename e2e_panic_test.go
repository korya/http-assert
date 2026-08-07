package main_test

import (
	"regexp"
	"strings"
	"testing"
)

// No malformed flag value may crash the process.
//
// The flag list is read from --help at run time rather than hard-coded, so a
// flag added later is probed automatically. That is the point: a fixed list
// would silently stop covering the CLI the moment it grew.

// hostileValues are inputs chosen to break parsers rather than to be rejected
// politely. Being rejected is a fine outcome -- crashing is not.
var hostileValues = []string{
	"[unclosed",                // unterminated character class
	"(unclosed",                // unterminated group
	"*",                        // quantifier with nothing to repeat
	"+",                        //
	"?",                        //
	`\`,                        // trailing escape
	`(?P<`,                     // truncated named group
	"a{2,1}",                   // inverted repetition bounds
	"%s%d%n",                   // format specifiers
	"../../../etc/passwd",      // traversal-shaped
	"-",                        // looks like a flag
	"--",                       // end-of-flags marker
	"=",                        //
	":::=====",                 // confuses the host-mapping split
	"a:1=b c:2=d",              // whitespace-separated pair
	"\n\t\r",                   // control whitespace
	"héllo wörld 日本語",          // multi-byte
	strings.Repeat("A", 10000), // long
	strings.Repeat("[", 500),   // deeply unbalanced
	"",                         // empty
}

// flagSpec is one flag as advertised by --help.
type flagSpec struct {
	Name     string // long name, without dashes
	TakesArg bool   // whether --help shows a value type after it
}

// cliFlags parses the Flags section of --help.
func cliFlags(t *testing.T) []flagSpec {
	t.Helper()

	r := run(t, nil, "--help")
	assertExit(t, r, exitOK)

	_, flagsSection, found := strings.Cut(r.Output(), "Flags:")
	if !found {
		t.Fatalf("--help has no Flags section; the parser below is stale\n%s", r.Output())
	}

	// e.g. "  -m, --max-time int   Maximum time ..." or "      --assert-ok   Assert ..."
	line, err := regexp.Compile(`^\s+(?:-[a-zA-Z], )?--([a-z-]+)(?: ([a-zA-Z]+))?\s{2,}`)
	if err != nil {
		t.Fatalf("bad flag-line pattern: %s", err)
	}

	var out []flagSpec
	for _, l := range strings.Split(flagsSection, "\n") {
		m := line.FindStringSubmatch(l)
		if m == nil || m[1] == "help" {
			continue
		}
		out = append(out, flagSpec{Name: m[1], TakesArg: m[2] != ""})
	}

	// Guards against a --help format change turning this into a no-op that
	// probes nothing and passes.
	if len(out) < 19 {
		t.Fatalf("parsed only %d flags from --help, expected at least 19 -- parser is stale", len(out))
	}

	return out
}

func TestE2ENoFlagPanics(t *testing.T) {
	flags := cliFlags(t)
	target := "http://127.0.0.1:1/refused" // nothing listens; the request fails fast

	for _, f := range flags {
		t.Run(f.Name, func(t *testing.T) {
			for _, v := range hostileValues {
				// A bool flag takes its value with '=' or not at all.
				//
				// Note that pflag rejects a non-boolean before the value reaches
				// any of this program's code, so for those flags this probe
				// mostly exercises the flag layer. Their own parsing paths are
				// covered by the config matrix and the assertion tests.
				args := []string{"--" + f.Name + "=" + v, target}
				if f.TakesArg {
					args = []string{"--" + f.Name, v, target}
				}

				r := run(t, nil, args...)
				assertNoPanic(t, r, "--"+f.Name, v)
			}
		})
	}
}

// TestE2ENoPanicFromEnvironment covers the other way a value reaches the parsers.
func TestE2ENoPanicFromEnvironment(t *testing.T) {
	for _, tc := range envSupportedCases(t) {
		t.Run(tc.Flag, func(t *testing.T) {
			for _, v := range hostileValues {
				r := run(t, map[string]string{tc.EnvKey: v},
					"--assert-ok", "http://127.0.0.1:1/refused")
				assertNoPanic(t, r, tc.EnvKey, v)
			}
		})
	}
}

// TestE2ENoPanicFromPositionalArgument covers the URL, which is not a flag.
func TestE2ENoPanicFromPositionalArgument(t *testing.T) {
	for _, v := range hostileValues {
		r := run(t, nil, "--assert-ok", v)
		assertNoPanic(t, r, "<URL>", v)
	}
}

// assertNoPanic fails only on a crash. Any exit code the CLI chooses
// deliberately is acceptable -- rejecting bad input is correct behaviour; the
// contract under test is merely that it is never reported as a Go panic.
func assertNoPanic(t *testing.T, r result, subject, value string) {
	t.Helper()

	const goPanicExitCode = 2

	if r.ExitCode == goPanicExitCode || strings.Contains(r.Output(), "panic:") ||
		strings.Contains(r.Output(), "goroutine ") {
		t.Fatalf("%s = %q crashed the process (exit %d)\n%s",
			subject, truncate(value), r.ExitCode, r.Output())
	}
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
