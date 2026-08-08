package main_test

import (
	"strings"
	"testing"
)

// Repeating an assertion flag used to depend on how the flag happened to be
// declared: the header flags accumulated, everything else silently kept the
// last value and threw the rest away. A run could go green having checked less
// than it was asked to, which is the worst thing a test tool can do (#35).
//
// The flag list comes from --help rather than a table here, so an assertion
// flag added later is held to the same rule the day it is added -- the absence
// of exactly that link is what let the two groups drift apart originally.

// accumulates mirrors the rule the CLI applies, using the type --help prints.
func accumulates(flagType string) bool {
	return strings.HasSuffix(flagType, "Array") || strings.HasSuffix(flagType, "Slice")
}

func TestE2ERepeatedAssertionFlags(t *testing.T) {
	// The config matrix already knows a valid invocation for every option,
	// including which endpoint makes that assertion meaningful.
	args := make(map[string]configCase, len(configCases(t)))
	for _, tc := range configCases(t) {
		args[tc.Flag] = tc
	}

	var checked int
	for _, f := range cliFlags(t) {
		if !strings.HasPrefix(f.Name, "assert-") {
			continue
		}

		tc, ok := args[f.Name]
		if !ok {
			t.Fatalf("--%s is advertised by --help but absent from the config matrix; add it there", f.Name)
		}
		checked++

		t.Run(f.Name, func(t *testing.T) {
			// The same option, twice, then whatever target makes it meaningful.
			var argv []string
			argv = append(argv, tc.CLI...)
			argv = append(argv, tc.CLI...)
			argv = append(argv, tc.Base...)

			r := run(t, nil, argv...)

			if accumulates(f.Type) {
				// Repeating these is the documented way to make several
				// assertions, so it must not be an error.
				if r.ExitCode == exitBadFlagVal {
					t.Fatalf("--%s (%s) rejected a repeat, but repeating it is how you add assertions\n%s",
						f.Name, f.Type, r.Output())
				}
				return
			}

			assertExit(t, r, exitBadFlagVal)
			assertContains(t, r, "--"+f.Name+" was given 2 times")
		})
	}

	// Guards against the --help parser or the prefix test quietly matching
	// nothing, which would leave this passing while checking no flag at all.
	if checked < 10 {
		t.Fatalf("checked only %d assertion flags, expected at least 10 -- the enumeration is stale", checked)
	}
}

// The contradiction in the issue: two --assert-status values, where the first
// would have failed. It used to exit 0, having run only the second.
func TestE2ERepeatedAssertionKeepsNothingSilently(t *testing.T) {
	r := run(t, nil, "--assert-status", "500", "--assert-status", "200", url("/ok"))

	assertExit(t, r, exitBadFlagVal)
	assertNotContains(t, r, "PASSED")
}
