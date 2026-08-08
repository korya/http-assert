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
// 6 of the 24 options honour the environment; the other 18 silently ignore it
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
		{
			Flag: "location", CLI: []string{"-L"},
			EnvKey: "HTTP_ASSERT_LOCATION", EnvVal: "true", EnvSupported: false, Issue: 54,
			// The 302 carries no body, so only a followed chain can satisfy this.
			Base:    []string{"--assert-body-eq", "arrived", url("/hop?n=1")},
			Applied: func(r result) bool { return r.ExitCode == exitOK },
		},
		{
			Flag: "max-redirs", CLI: []string{"--max-redirs", "1"},
			EnvKey: "HTTP_ASSERT_MAX_REDIRS", EnvVal: "1", EnvSupported: false, Issue: 54,
			// Three hops: within the default of 10, outside a bound of 1. The
			// option needs -L to be accepted at all, so -L lives in Base.
			Base: []string{"-L", "--assert-ok", url("/hop?n=3")},
			Applied: func(r result) bool {
				return strings.Contains(r.Output(), "redirect chain was not followed")
			},
		},

		{
			Flag: "retry", CLI: []string{"--retry", "1"},
			EnvKey: "HTTP_ASSERT_RETRY", EnvVal: "1", EnvSupported: false, Issue: 54,
			// A permanently failing endpoint, so the run is decided by whether a
			// second attempt happened at all rather than by what it returned.
			Base:    []string{"--assert-ok", url("/500")},
			Applied: func(r result) bool { return strings.Contains(r.Output(), "[~] retry ") },
		},
		{
			Flag: "retry-delay", CLI: []string{"--retry-delay", "50ms"},
			EnvKey: "HTTP_ASSERT_RETRY_DELAY", EnvVal: "50ms", EnvSupported: false, Issue: 54,
			// The delay is announced before it is waited out, which is the only
			// way to observe the value without timing the process. --retry lives
			// in Base because the option is not accepted without it.
			Base:    []string{"--retry", "1", "--assert-ok", url("/500")},
			Applied: func(r result) bool { return strings.Contains(r.Output(), "in 50ms") },
		},
		{
			Flag: "retry-max-time", CLI: []string{"--retry-max-time", "1ms"},
			EnvKey: "HTTP_ASSERT_RETRY_MAX_TIME", EnvVal: "1ms", EnvSupported: false, Issue: 54,
			// A budget too small for even one delay, so it always wins over the
			// count -- and the message names whichever bound ended the run.
			Base: []string{"--retry", "3", "--retry-delay", "50ms", "--assert-ok", url("/500")},
			Applied: func(r result) bool {
				return strings.Contains(r.Output(), "--retry-max-time is 1ms")
			},
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
	if got, want := len(cases), 24; got != want {
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
						tc.EnvKey+" is ignored; only 6 of 24 options read the environment")
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

// The tests below pin value-level semantics that TestE2EConfigContract does not
// reach. That matrix asks "did this option take effect?" -- a boolean -- so it
// cannot see how a value was parsed, only that it was. Everything here exists
// because removing viper (#54) turns on exactly these paths.

// envSupportedCases returns the subset of the matrix whose options read the
// environment, so the value-level tests stay in step with it automatically.
func envSupportedCases(t *testing.T) []configCase {
	t.Helper()

	var out []configCase
	for _, tc := range configCases(t) {
		if tc.EnvSupported {
			out = append(out, tc)
		}
	}
	if len(out) != 6 {
		t.Fatalf("expected 6 environment-backed options, got %d", len(out))
	}
	return out
}

// TestE2EConfigEnvEmptyMeansUnset pins that an empty variable is ignored rather
// than parsed. viper's AllowEmptyEnv defaults to false, so HTTP_ASSERT_MAX_TIME=""
// resolves to the flag default (20) and not to zero.
//
// The distinction is mostly invisible from outside -- every option's default
// happens to coincide with its zero value, and 0 vs 20 seconds cannot be told
// apart without a 20-second request. What this test does guarantee is that an
// empty variable never becomes an *error*, which is the regression that would
// otherwise slip through when unparseable values start being rejected.
func TestE2EConfigEnvEmptyMeansUnset(t *testing.T) {
	for _, tc := range envSupportedCases(t) {
		t.Run(tc.Flag, func(t *testing.T) {
			r := run(t, map[string]string{tc.EnvKey: ""}, "--assert-ok", url("/ok"))
			assertExit(t, r, exitOK)
			assertNotContains(t, r, "Invalid value")
		})
	}
}

// TestE2EConfigEnvInvalidValues pins that an unparseable variable is rejected.
//
// It used to be cast to the type's zero value and applied silently. The
// MAX_TIME case is why that changed: "abc" became 0, and a zero
// http.Client.Timeout means no timeout at all, so a typo quietly removed the
// deadline from a tool whose entire job is enforcing one.
func TestE2EConfigEnvInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		Name string
		Env  map[string]string
		Args []string
		Diag string
	}{
		{
			Name: "max-time is not a number",
			Env:  map[string]string{"HTTP_ASSERT_MAX_TIME": "abc"},
			Diag: `Invalid value for HTTP_ASSERT_MAX_TIME="abc"`,
		},
		{
			Name: "max-time is a float",
			Env:  map[string]string{"HTTP_ASSERT_MAX_TIME": "1.5"},
			Diag: `Invalid value for HTTP_ASSERT_MAX_TIME="1.5"`,
		},
		{
			Name: "insecure is not a boolean",
			Env:  map[string]string{"HTTP_ASSERT_INSECURE": "garbage"},
			Diag: `Invalid value for HTTP_ASSERT_INSECURE="garbage"`,
		},
		{
			Name: "verbose is not a boolean",
			Env:  map[string]string{"HTTP_ASSERT_VERBOSE": "garbage"},
			Diag: `Invalid value for HTTP_ASSERT_VERBOSE="garbage"`,
		},
		{
			Name: "silent is not a boolean",
			Env:  map[string]string{"HTTP_ASSERT_SILENT": "maybe"},
			Diag: `Invalid value for HTTP_ASSERT_SILENT="maybe"`,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := run(t, tc.Env, append(tc.Args, "--assert-ok", url("/ok"))...)
			assertExit(t, r, exitBadFlagVal)
			assertContains(t, r, tc.Diag)
			// The reason is passed through rather than swallowed.
			assertContains(t, r, "invalid syntax")
		})
	}

	// log-level and maphost are string-valued, so the environment layer accepts
	// them and their own parsers reject them. Same exit code, different message.
	t.Run("log-level is validated by the level parser", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_LOG_LEVEL": "trace"}, "--assert-ok", url("/ok"))
		assertExit(t, r, exitBadFlagVal)
		assertContains(t, r, `Invalid value for --log-level flag: "trace"`)
	})

	t.Run("maphost is validated by the mapping parser", func(t *testing.T) {
		r := run(t, map[string]string{"HTTP_ASSERT_MAPHOST": "garbage"}, "--assert-ok", url("/ok"))
		assertExit(t, r, exitBadFlagVal)
		assertContains(t, r, "Invalid value for --maphost flag")
	})
}

// TestE2EConfigEnvBooleanForms pins which spellings of true and false are
// understood. strconv.ParseBool and viper's cast agree on the canonical forms
// and disagree on "yes"/"on", which viper silently reads as false.
func TestE2EConfigEnvBooleanForms(t *testing.T) {
	// Debug logging is observable, so verbose is the probe.
	base := []string{"--maphost", "mapped.invalid:80=" + hostPort(),
		"--assert-ok", "http://mapped.invalid/ok"}

	t.Run("accepted", func(t *testing.T) {
		for _, tc := range []struct {
			Value string
			On    bool
		}{
			{"true", true}, {"1", true}, {"TRUE", true}, {"True", true}, {"t", true},
			{"false", false}, {"0", false}, {"FALSE", false}, {"f", false},
		} {
			t.Run(tc.Value, func(t *testing.T) {
				r := run(t, map[string]string{"HTTP_ASSERT_VERBOSE": tc.Value}, base...)
				assertExit(t, r, exitOK)
				if got := strings.Contains(r.Output(), "HostMappings"); got != tc.On {
					t.Fatalf("HTTP_ASSERT_VERBOSE=%q: verbose=%v, want %v\n%s",
						tc.Value, got, tc.On, r.Output())
				}
			})
		}
	})

	// Shell-style spellings are not Go booleans. They used to be read as false,
	// which meant `HTTP_ASSERT_VERBOSE=yes` quietly turned verbosity off.
	t.Run("rejected", func(t *testing.T) {
		for _, v := range []string{"yes", "on", "no", "off", "y", "n"} {
			t.Run(v, func(t *testing.T) {
				r := run(t, map[string]string{"HTTP_ASSERT_VERBOSE": v}, base...)
				assertExit(t, r, exitBadFlagVal)
				assertContains(t, r, "Invalid value for HTTP_ASSERT_VERBOSE")
			})
		}
	})
}

// TestE2EConfigPrecedenceAllOptions extends the precedence guarantee from
// max-time to every environment-backed option. Each case sets the flag
// explicitly to the value the environment is trying to override, so a build
// that resolved the environment first would visibly lose.
func TestE2EConfigPrecedenceAllOptions(t *testing.T) {
	mapping := "mapped.invalid:80=" + hostPort()
	target := "http://mapped.invalid/ok"

	for _, tc := range []struct {
		Flag  string
		CLI   []string
		Env   map[string]string
		Base  []string
		Check func(t *testing.T, r result)
	}{
		{
			Flag: "verbose", CLI: []string{"--verbose=false"},
			Env:   map[string]string{"HTTP_ASSERT_VERBOSE": "true"},
			Base:  []string{"--maphost", mapping, "--assert-ok", target},
			Check: func(t *testing.T, r result) { assertNotContains(t, r, "HostMappings") },
		},
		{
			Flag: "silent", CLI: []string{"--silent=false"},
			Env:   map[string]string{"HTTP_ASSERT_SILENT": "true"},
			Base:  []string{"--assert-ok", url("/ok")},
			Check: func(t *testing.T, r result) { assertContains(t, r, "PASSED") },
		},
		{
			Flag: "log-level", CLI: []string{"--log-level", "debug"},
			Env:   map[string]string{"HTTP_ASSERT_LOG_LEVEL": "error"},
			Base:  []string{"--maphost", mapping, "--assert-ok", target},
			Check: func(t *testing.T, r result) { assertContains(t, r, "HostMappings") },
		},
		{
			Flag: "insecure", CLI: []string{"--insecure=false"},
			Env:   map[string]string{"HTTP_ASSERT_INSECURE": "true"},
			Base:  []string{"--assert-ok", tlsSrv.URL},
			Check: func(t *testing.T, r result) { assertContains(t, r, "certificate") },
		},
		{
			Flag: "maphost", CLI: []string{"--maphost", "decoy.invalid:80=127.0.0.1:9"},
			Env:  map[string]string{"HTTP_ASSERT_MAPHOST": mapping},
			Base: []string{"--assert-ok", target},
			// The command-line mapping does not cover mapped.invalid, so the
			// host must fail to resolve rather than reach the test server.
			Check: func(t *testing.T, r result) { assertContains(t, r, "failed to send request") },
		},
	} {
		t.Run(tc.Flag, func(t *testing.T) {
			tc.Check(t, run(t, tc.Env, append(append([]string{}, tc.CLI...), tc.Base...)...))
		})
	}
}

// TestE2EConfigEnvLogLevels pins every level reachable through the environment.
// TestE2ELogLevels covers the same values on the command line; without this the
// environment path is only ever exercised at "error".
func TestE2EConfigEnvLogLevels(t *testing.T) {
	mapping := "mapped.invalid:80=" + hostPort()
	target := "http://mapped.invalid/ok"

	for _, tc := range []struct {
		Level  string
		Debug  bool // the host-mapping summary is debug-only
		Result bool // the PASSED line is info and above
	}{
		{"debug", true, true},
		{"info", false, true},
		{"warn", false, false},
		{"error", false, false},
	} {
		t.Run(tc.Level, func(t *testing.T) {
			r := run(t, map[string]string{"HTTP_ASSERT_LOG_LEVEL": tc.Level},
				"--maphost", mapping, "--assert-ok", target)
			assertExit(t, r, exitOK)

			if got := strings.Contains(r.Output(), "HostMappings"); got != tc.Debug {
				t.Fatalf("level %q: debug output=%v, want %v", tc.Level, got, tc.Debug)
			}
			if got := strings.Contains(r.Output(), "PASSED"); got != tc.Result {
				t.Fatalf("level %q: result line=%v, want %v", tc.Level, got, tc.Result)
			}
		})
	}
}
