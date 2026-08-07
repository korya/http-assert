package main_test

import (
	"strings"
	"testing"
)

// TestE2EConfigContract is the executable specification of how http-assert
// resolves configuration. It exists so that removing viper (#54) can be
// verified rather than hoped: run it before the change, run it after, and any
// difference is a regression.
//
// Every option is exercised three ways -- unset, set on the command line, and
// set through the environment -- and the outcome is pinned. Precedence between
// the two sources is covered separately by TestE2EConfigPrecedence.
//
// EnvSupported records what the tool does *today*, not what it should do. Only
// 6 of the 19 options honour the environment; the other 13 silently ignore it
// (#54 proposes making this uniform). When that lands, flip those booleans --
// the diff is the proof the change did what it claimed.

type configCase struct {
	// Flag is the long flag name, without leading dashes.
	Flag string
	// CLI sets the option on the command line.
	CLI []string
	// EnvKey/EnvVal set the same option through the environment.
	EnvKey string
	EnvVal string
	// EnvSupported is whether EnvKey currently has any effect at all.
	EnvSupported bool
	// Issue, when non-zero, is the GitHub issue tracking the fact that
	// EnvSupported is false when it arguably should be true.
	Issue int
	// Base is the rest of the invocation, appended after the option under test.
	Base []string
	// Applied reports whether the option visibly took effect.
	Applied func(r result) bool
}

// noAssertions is the error the CLI emits when no --assert-* flag was parsed.
// For every assertion option, "did it apply?" reduces to "is this absent?".
const noAssertions = "no assertions defined"

func assertionApplied(r result) bool { return !strings.Contains(r.Output(), noAssertions) }

func configCases(t *testing.T) []configCase {
	t.Helper()

	okURL := url("/ok")
	mapping := "mapped.invalid:80=" + hostPort()

	// An assertion option is "applied" when the CLI stops complaining that it
	// has nothing to check. Every assertion flag shares this shape.
	assertion := func(flag string, cli []string, env, target string) configCase {
		return configCase{
			Flag: flag, CLI: cli,
			EnvKey: env, EnvVal: cli[len(cli)-1], EnvSupported: false, Issue: 54,
			Base: []string{target}, Applied: assertionApplied,
		}
	}

	return []configCase{
		// ---- options wired through viper: the environment works ----
		{
			Flag: "verbose", CLI: []string{"-v"},
			EnvKey: "HTTP_ASSERT_VERBOSE", EnvVal: "true", EnvSupported: true,
			Base: []string{"--maphost", mapping, "--assert-ok", "http://mapped.invalid/ok"},
			// The host-mapping summary is logged at debug level only.
			Applied: func(r result) bool { return strings.Contains(r.Output(), "HostMappings") },
		},
		{
			Flag: "silent", CLI: []string{"-s"},
			EnvKey: "HTTP_ASSERT_SILENT", EnvVal: "true", EnvSupported: true,
			Base:    []string{"--assert-ok", okURL},
			Applied: func(r result) bool { return r.Output() == "" },
		},
		{
			Flag: "log-level", CLI: []string{"--log-level", "error"},
			EnvKey: "HTTP_ASSERT_LOG_LEVEL", EnvVal: "error", EnvSupported: true,
			Base:    []string{"--assert-ok", okURL},
			Applied: func(r result) bool { return r.Output() == "" },
		},
		{
			Flag: "insecure", CLI: []string{"-k"},
			EnvKey: "HTTP_ASSERT_INSECURE", EnvVal: "true", EnvSupported: true,
			Base: []string{"--assert-ok", tlsSrv.URL},
			// The certificate is self-signed, so success *is* the observation.
			Applied: func(r result) bool { return r.ExitCode == exitOK },
		},
		{
			Flag: "max-time", CLI: []string{"-m", "1"},
			EnvKey: "HTTP_ASSERT_MAX_TIME", EnvVal: "1", EnvSupported: true,
			Base:    []string{"--assert-ok", url("/slow")},
			Applied: func(r result) bool { return strings.Contains(r.Output(), "Client.Timeout") },
		},
		{
			Flag: "maphost", CLI: []string{"--maphost", mapping},
			EnvKey: "HTTP_ASSERT_MAPHOST", EnvVal: mapping, EnvSupported: true,
			Base: []string{"--assert-ok", "http://mapped.invalid/ok"},
			// Without the mapping the host does not resolve at all.
			Applied: func(r result) bool { return r.ExitCode == exitOK },
		},

		// ---- options read straight off cobra: the environment is ignored ----
		{
			Flag: "request", CLI: []string{"-X", "POST"},
			EnvKey: "HTTP_ASSERT_REQUEST", EnvVal: "POST", EnvSupported: false, Issue: 54,
			// A deliberately failing assertion makes the CLI dump the response,
			// which is where the echoed method becomes observable.
			Base:    []string{"--assert-body-eq", "never-matches", url("/echo")},
			Applied: func(r result) bool { return strings.Contains(r.Output(), `\"method\":\"POST\"`) },
		},
		{
			Flag: "header", CLI: []string{"-H", "X-Probe: 1"},
			EnvKey: "HTTP_ASSERT_HEADER", EnvVal: "X-Probe: 1", EnvSupported: false, Issue: 54,
			Base:    []string{"--assert-body-eq", "never-matches", url("/echo")},
			Applied: func(r result) bool { return strings.Contains(r.Output(), "X-Probe") },
		},
		{
			Flag: "data", CLI: []string{"-d", "probe-payload"},
			EnvKey: "HTTP_ASSERT_DATA", EnvVal: "probe-payload", EnvSupported: false, Issue: 54,
			Base:    []string{"--assert-body-eq", "never-matches", url("/echo")},
			Applied: func(r result) bool { return strings.Contains(r.Output(), "probe-payload") },
		},

		// ---- assertion options: all cobra-only ----
		assertion("assert-ok", []string{"--assert-ok"}, "HTTP_ASSERT_ASSERT_OK", okURL),
		assertion("assert-status", []string{"--assert-status", "200"}, "HTTP_ASSERT_ASSERT_STATUS", okURL),
		assertion("assert-header", []string{"--assert-header", `Cache-Control: max-age=\d+`}, "HTTP_ASSERT_ASSERT_HEADER", okURL),
		assertion("assert-header-eq", []string{"--assert-header-eq", "X-Api-Version: v1"}, "HTTP_ASSERT_ASSERT_HEADER_EQ", okURL),
		assertion("assert-header-missing", []string{"--assert-header-missing", "X-Absent"}, "HTTP_ASSERT_ASSERT_HEADER_MISSING", okURL),
		assertion("assert-body", []string{"--assert-body", `"status"`}, "HTTP_ASSERT_ASSERT_BODY", okURL),
		assertion("assert-body-eq", []string{"--assert-body-eq", `{"status":"success","users":[]}`}, "HTTP_ASSERT_ASSERT_BODY_EQ", okURL),
		assertion("assert-body-empty", []string{"--assert-body-empty"}, "HTTP_ASSERT_ASSERT_BODY_EMPTY", url("/empty")),
		assertion("assert-redirect", []string{"--assert-redirect", `https://.*\.com/.*`}, "HTTP_ASSERT_ASSERT_REDIRECT", url("/redirect")),
		assertion("assert-redirect-eq", []string{"--assert-redirect-eq", "https://new-domain.com/path"}, "HTTP_ASSERT_ASSERT_REDIRECT_EQ", url("/redirect")),
	}
}

func TestE2EConfigContract(t *testing.T) {
	cases := configCases(t)

	// Guards against an option being added to the CLI and quietly skipped here.
	if got, want := len(cases), 19; got != want {
		t.Fatalf("config matrix covers %d options, want %d -- add the new flag to configCases", got, want)
	}

	for _, tc := range cases {
		t.Run(tc.Flag, func(t *testing.T) {
			t.Run("unset", func(t *testing.T) {
				r := run(t, nil, tc.Base...)
				if tc.Applied(r) {
					t.Fatalf("%s took effect with neither flag nor env set\n%s", tc.Flag, r.Output())
				}
			})

			t.Run("cli", func(t *testing.T) {
				r := run(t, nil, append(append([]string{}, tc.CLI...), tc.Base...)...)
				if !tc.Applied(r) {
					t.Fatalf("%s did not take effect via the command line\n%s", tc.Flag, r.Output())
				}
			})

			t.Run("env", func(t *testing.T) {
				if !tc.EnvSupported {
					characterizes(t, tc.Issue,
						tc.EnvKey+" is ignored; only 6 of 19 options read the environment")
				}
				r := run(t, map[string]string{tc.EnvKey: tc.EnvVal}, tc.Base...)
				if got := tc.Applied(r); got != tc.EnvSupported {
					t.Fatalf("%s via %s: applied=%v, want %v\n%s",
						tc.Flag, tc.EnvKey, got, tc.EnvSupported, r.Output())
				}
			})
		})
	}
}

// TestE2EConfigPrecedence pins the resolution order between the two sources.
// It is tested on max-time because the value is directly observable: a 1s
// budget cannot survive the 2s /slow endpoint, and a 20s budget always can.
func TestE2EConfigPrecedence(t *testing.T) {
	slow := url("/slow")

	timedOut := func(r result) bool { return strings.Contains(r.Output(), "Client.Timeout") }

	t.Run("command line beats environment", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAX_TIME": "1"}, "-m", "20", "--assert-ok", slow)
		if timedOut(r) {
			t.Fatalf("env won over the command line; the request timed out\n%s", r.Output())
		}
		assertExit(t, r, exitOK)
	})

	// The mirror image. Without it, a build that simply ignored the environment
	// would also pass the case above.
	t.Run("command line beats environment, reversed", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAX_TIME": "20"}, "-m", "1", "--assert-ok", slow)
		if !timedOut(r) {
			t.Fatalf("env won over the command line; the request completed\n%s", r.Output())
		}
		assertExit(t, r, exitRequestFail)
	})

	t.Run("environment beats the built-in default", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAX_TIME": "1"}, "--assert-ok", slow)
		if !timedOut(r) {
			t.Fatalf("env did not override the 20s default\n%s", r.Output())
		}
	})
}

// TestE2EConfigEnvSliceSeparator pins how a repeatable option is expressed in a
// single environment variable.
//
// This is the least obvious part of the whole contract and the most likely
// casualty of #54: viper splits on WHITESPACE, not commas. A reimplementation
// reaching for strings.Split(v, ",") -- the intuitive choice -- would break
// every multi-mapping user, and nothing else in the suite would notice.
func TestE2EConfigEnvSliceSeparator(t *testing.T) {
	// The first mapping points nowhere; only the second can satisfy the request.
	// So a pass proves both entries were parsed, not just the leading one.
	decoy := "decoy.invalid:80=127.0.0.1:9"
	real := "mapped.invalid:80=" + hostPort()
	target := "http://mapped.invalid/ok"

	t.Run("whitespace separates values", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAPHOST": decoy + " " + real},
			"--assert-ok", target)
		assertExit(t, r, exitOK)
	})

	t.Run("commas do not separate values", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAPHOST": decoy + "," + real},
			"--assert-ok", target)
		// The whole string is taken as one mapping, whose destination port is
		// then unparseable.
		assertExit(t, r, exitBadFlagVal)
		assertContains(t, r, "Invalid value for --maphost flag")
	})

	t.Run("a single value needs no separator", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAPHOST": real}, "--assert-ok", target)
		assertExit(t, r, exitOK)
	})
}
