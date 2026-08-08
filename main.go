// Command http-assert performs an HTTP request and asserts properties of the
// response, exiting non-zero when an assertion fails.
//
// It is built for the places where a failing exit code is the whole point: a
// pipeline step, a container healthcheck, a monitoring script. Assertions are
// declared as flags, and the request is made once and checked against all of
// them.
//
// The three header assertions can be repeated to assert several things at
// once. Every other assertion flag takes a single value and is rejected if
// given twice, rather than quietly keeping the last one.
//
//	http-assert --assert-ok https://example.com
//	http-assert --assert-status 201 -X POST -d '{"n":1}' https://api.example.com/things
//	http-assert --assert-header 'Content-Type: application/json' \
//		--assert-body '"ok":true' https://api.example.com/health
//
// # Exit codes
//
// The exit code is the result. Distinguishing a failed assertion from a tool
// that could not run is what lets a pipeline tell a broken service from a
// broken invocation.
//
//	0    every assertion passed
//	71   a flag or environment value was rejected
//	91   the request could not be constructed from the method and URL
//	93   the request failed, or at least one assertion did
//	103  wrong argument count, or an unknown flag
//
// # Environment
//
// Six options also read the environment, as HTTP_ASSERT_<NAME> with dashes
// replaced by underscores: --verbose, --silent, --log-level, --insecure,
// --max-time and --maphost. The command line wins over the environment, which
// wins over the default. A value that does not parse is rejected rather than
// coerced, so a typo fails loudly instead of silently disabling the option it
// was meant to set.
//
// The remaining options are command-line only. Repeatable options split one
// variable on whitespace, which suits host mappings and would corrupt header
// values.
//
// # Redirects
//
// Redirects are not followed by default. A 3xx reaches the assertions as it
// stands, which is the only reason --assert-redirect and --assert-redirect-eq
// can work: a followed redirect leaves no Location header behind to assert on.
//
// --location follows the chain instead, and every assertion then applies to the
// response at the end of it. --max-redirs bounds the chain. Because following
// consumes the 3xx, --location and the two redirect assertions are mutually
// exclusive rather than merely unusual together.
//
// # Retries
//
// --retry re-sends the request after a failed attempt, waiting --retry-delay
// between them. A failure is any failure: a transport error, or an assertion
// that did not hold. Waiting for a service to come up is the case this exists
// for, and there the response arrives perfectly well and says the wrong thing.
//
// The delay is fixed rather than exponential, so --retry 30 --retry-delay 1s
// reads as "poll once a second for thirty seconds" and the worst case can be
// worked out without a calculator.
//
// --max-time bounds each attempt; --retry-max-time bounds the whole run. The
// latter is checked before each retry, so an attempt already in flight can
// overrun it by up to one --max-time.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func main() {
	cmd := &cobra.Command{
		Use:   "http-assert <URL>",
		Short: "Perform HTTP request and assert received HTTP response",
		// The exit code is the whole product, so it is documented where a
		// person actually looks for it. The environment is here for the same
		// reason: nothing else at the terminal reveals that six of these
		// options can be set without touching the command line.
		//
		// Careful with the wording below: the end-to-end suite locates the
		// flag list by cutting this output at the first "Flags:", so that
		// exact string must not appear here (see e2e_panic_test.go).
		Long: `Perform an HTTP request and assert properties of the response.

Assertions are declared as flags. The request is made once and checked
against all of them, and every failure is reported, not just the first.

Repeat --assert-header, --assert-header-eq or --assert-header-missing to make
several assertions of that kind. Every other assertion flag takes a single
value and is rejected if given twice, rather than quietly keeping the last.

Exit codes:
  0    every assertion passed
  71   a flag or environment value was rejected
  91   the request could not be constructed from the method and URL
  93   the request failed, or at least one assertion did
  103  wrong argument count, or an unknown flag

Environment:
  Six options can also be set as HTTP_ASSERT_<NAME>, with dashes replaced by
  underscores: HTTP_ASSERT_VERBOSE, HTTP_ASSERT_SILENT, HTTP_ASSERT_LOG_LEVEL,
  HTTP_ASSERT_INSECURE, HTTP_ASSERT_MAX_TIME and HTTP_ASSERT_MAPHOST. Every
  other option is command-line only.

  The command line wins over the environment, which wins over the default. An
  empty variable counts as unset, and a value that does not parse is rejected
  rather than quietly replaced by a zero.

  HTTP_PROXY, HTTPS_PROXY and NO_PROXY are honoured for the request itself.
  There is no flag for them.

Redirects:
  By default a 3xx response is asserted on as it stands and the redirect is
  not followed. That is what --assert-redirect and --assert-redirect-eq
  assert against.

  This is the opposite of curl's default, and --assert-ok counts a 3xx as
  success, so a redirecting endpoint passes a health check without the
  resource behind it ever being fetched.

  -L follows the chain, and every assertion then applies to the response at
  the end of it. --max-redirs bounds the chain and needs -L to mean anything.
  -L cannot be combined with --assert-redirect or --assert-redirect-eq: the
  3xx those inspect is the very thing -L consumes.

Retries:
  --retry re-sends the request after a failed attempt, waiting --retry-delay
  between them (1s by default, and fixed rather than exponential). A failure is
  any failure: a transport error, or an assertion that did not hold. Waiting
  for a service to come up is what this is for, and there the response arrives
  perfectly well and says the wrong thing.

  -m bounds each attempt, not the run. --retry-max-time bounds the run and is
  checked before each retry, so an attempt already in flight can overrun it.
  Both --retry-delay and --retry-max-time need --retry to mean anything.`,
		Example: `  # A health check: any non-error status passes
  http-assert --assert-ok https://example.com/health

  # Wait for a service to come up, polling once a second for half a minute
  http-assert --retry 30 --retry-delay 1s --assert-ok https://example.com/health

  # Exact status plus a body pattern
  http-assert --assert-status 201 --assert-body '"id":\s*[0-9]+' https://example.com/things

  # Send a request to a specific backend, as curl --resolve does
  http-assert --maphost 'example.com:443=127.0.0.1:8443' --assert-ok https://example.com/

  # Follow the redirect chain and assert on where it lands
  http-assert -L --assert-status 200 https://example.com/old`,
		Version: versionString(),
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			insecure, _ := cmd.Flags().GetBool("insecure")
			maxTime, _ := cmd.Flags().GetInt("max-time")
			maphost, _ := cmd.Flags().GetStringArray("maphost")
			location, _ := cmd.Flags().GetBool("location")
			maxRedirs, _ := cmd.Flags().GetInt("max-redirs")
			retry, _ := cmd.Flags().GetInt("retry")
			retryDelay, _ := cmd.Flags().GetDuration("retry-delay")
			retryMaxTime, _ := cmd.Flags().GetDuration("retry-max-time")
			c := Client{
				LogLevel:        mustParseLogLevel(cmd),
				SkipSslChecks:   insecure,
				Timeout:         time.Duration(maxTime) * time.Second,
				HostMappings:    mustParseHostMappings(maphost),
				FollowRedirects: location,
				MaxRedirects:    maxRedirs,
				Retries:         retry,
				RetryDelay:      retryDelay,
				RetryMaxTime:    retryMaxTime,
			}
			c.Init()

			m, _ := cmd.Flags().GetString("request")
			b := io.Reader(http.NoBody)
			if d, _ := cmd.Flags().GetString("data"); d != "" {
				b = strings.NewReader(d)
			}
			req, err := http.NewRequestWithContext(cmd.Context(), m, args[0], b)
			if err != nil {
				dief(91, "Cannot create request '%s %s': %s", m, args[0], err)
			}

			vs, _ := cmd.Flags().GetStringArray("header")
			for _, v := range vs {
				name, value := parseHeaderLine(v)
				req.Header.Add(name, value)
			}
			if err := c.Do(req, parseAssertionFlags(cmd)...); err != nil {
				dief(93, "Cannot perform request: %s", err)
			}
		},
	}
	// Deviations from curl's --resolve:
	// - use `=` to separate src and dst
	// - add [:dstport]
	cmd.PersistentFlags().StringArray("maphost", nil,
		"Provide a custom address for a specific host and port pair; "+
			"e.g. <srchostname:srcport=dsthostname[:dstport]>")
	cmd.PersistentFlags().BoolP("verbose", "v", false,
		"Be verbose; log debug messages (same as --log-level debug)")
	cmd.PersistentFlags().BoolP("silent", "s", false,
		"Be silent; log error messages only (same as --log-level error)")
	cmd.PersistentFlags().String("log-level", "",
		"Set log level; possible values: debug, info (default), warn, error")
	cmd.PersistentFlags().BoolP("insecure", "k", false, "Disable checking SSL certificates")
	cmd.PersistentFlags().IntP("max-time", "m", 20,
		"Maximum time in seconds that you allow each request to take")
	cmd.Flags().StringP("request", "X", "GET", "Set method for HTTP request")
	cmd.Flags().StringArrayP("header", "H", nil, "Set header for HTTP request")
	cmd.Flags().StringP("data", "d", "",
		"Sends the specified data in a POST request to the HTTP server")
	cmd.Flags().BoolP("location", "L", false,
		"Follow redirects; assertions then apply to the end of the chain")
	cmd.Flags().Int("max-redirs", 10,
		"Maximum number of redirects to follow; requires --location")
	cmd.Flags().Int("retry", 0,
		"Number of times to retry a failed attempt; 0 makes the request once")
	cmd.Flags().Duration("retry-delay", time.Second,
		"Fixed delay between attempts, e.g. 1s or 250ms; requires --retry")
	cmd.Flags().Duration("retry-max-time", 0,
		"Stop retrying after this long; 0 means only --retry bounds it; requires --retry")
	registerAssertionFlags(cmd)
	rejectRepeats(cmd.Flags())

	cmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		// Before applyEnv, so that a value the environment supplies can never
		// be mistaken for a second occurrence on the command line.
		checkRepeats(cmd.Flags())
		applyEnv(cmd.Flags())
		checkRedirectFlags(cmd.Flags())
		checkRetryFlags(cmd.Flags())
	}

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		dief(103, "%s", err)
	}
}

// envFlags are the options that can also be set through the environment, as
// HTTP_ASSERT_<NAME> with dashes replaced by underscores.
//
// Deliberately a list rather than every flag. Repeatable options take several
// values from one variable by splitting on whitespace, which suits host
// mappings but would corrupt header values, and those routinely contain spaces.
var envFlags = []string{"verbose", "silent", "log-level", "insecure", "max-time", "maphost"}

// applyEnv fills in the options the caller did not pass on the command line.
// Precedence is command line, then environment, then the flag default.
//
// A value that does not parse is rejected rather than coerced. The previous
// implementation cast it to the type's zero value, which meant a typo in
// HTTP_ASSERT_MAX_TIME produced a zero http.Client.Timeout -- that is, no
// timeout at all -- in a tool whose purpose is enforcing one.
func applyEnv(fs *pflag.FlagSet) {
	for _, name := range envFlags {
		f := fs.Lookup(name)
		if f == nil || f.Changed {
			continue // the command line wins
		}

		key := "HTTP_ASSERT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		v, ok := os.LookupEnv(key)
		if !ok || v == "" {
			continue // an empty variable counts as unset
		}

		if sv, isSlice := f.Value.(pflag.SliceValue); isSlice {
			_ = sv.Replace(strings.Fields(v))
			continue
		}

		if err := f.Value.Set(v); err != nil {
			dief(71, "Invalid value for %s=%q: %s", key, v, err)
		}
	}
}

// checkRedirectFlags rejects the three ways --location and --max-redirs can be
// asked for something they cannot deliver.
//
// The first is the one worth refusing loudly. --assert-redirect* inspects the
// 3xx, and --location is the instruction to consume it, so the pair is a
// contradiction rather than a combination. Resolving it silently either way
// leaves a run reporting success for a check it never made.
//
// The other two are smaller versions of the same thing: a --max-redirs nobody
// will read because nothing is being followed, and a negative bound that would
// refuse the redirect it was meant to permit. Zero is allowed and means what it
// means in curl -- follow none.
func checkRedirectFlags(fs *pflag.FlagSet) {
	follow, _ := fs.GetBool("location")

	if follow {
		for _, name := range []string{"assert-redirect", "assert-redirect-eq"} {
			if fs.Changed(name) {
				dief(71, "Flags --location and --%s cannot be used together: "+
					"--location follows the redirect, which consumes the 3xx "+
					"response --%s inspects", name, name)
			}
		}
	}

	if !fs.Changed("max-redirs") {
		return
	}
	if !follow {
		dief(71, "Flag --max-redirs bounds a redirect chain that is not being "+
			"followed; pass --location, or drop --max-redirs")
	}
	if n, _ := fs.GetInt("max-redirs"); n < 0 {
		dief(71, "Invalid value for --max-redirs flag: %d; it counts redirects, "+
			"so the smallest meaningful value is 0", n)
	}
}

// checkRetryFlags rejects the ways the retry options can be asked for something
// they cannot deliver, on the same grounds as checkRedirectFlags above.
//
// The two durations are inert without --retry, exactly as --max-redirs is inert
// without --location: a value nobody will read. The negative cases are worse
// than inert, because pflag accepts a negative duration without complaint --
// --retry-delay=-2s parses fine and then never waits.
//
// The test for the durations is whether --retry was named, not what it was set
// to. `--retry ${RETRIES:-0} --retry-delay 1s` is an ordinary shape for a
// script, and refusing it would break a caller who did nothing wrong.
func checkRetryFlags(fs *pflag.FlagSet) {
	if n, _ := fs.GetInt("retry"); n < 0 {
		dief(71, "Invalid value for --retry flag: %d; it counts retries, so the "+
			"smallest meaningful value is 0", n)
	}

	for _, name := range []string{"retry-delay", "retry-max-time"} {
		if !fs.Changed(name) {
			continue
		}
		if !fs.Changed("retry") {
			dief(71, "Flag --%s configures retrying that is not switched on; "+
				"pass --retry, or drop --%s", name, name)
		}
		if d, _ := fs.GetDuration(name); d < 0 {
			dief(71, "Invalid value for --%s flag: %s; it is a length of time, "+
				"so the smallest meaningful value is 0", name, d)
		}
	}
}

// dief formats a message to stderr and terminates the process with rc.
func dief(rc int, format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, "\nError: "+format, args...)
	os.Exit(rc)
}

type LogLevel int

const (
	LError LogLevel = iota
	LWarn
	LInfo
	LDebug
)

func mustParseLogLevel(cmd *cobra.Command) LogLevel {
	levelStr, _ := cmd.Flags().GetString("log-level")
	if levelStr == "" {
		if v, _ := cmd.Flags().GetBool("verbose"); v {
			return LDebug
		}

		if v, _ := cmd.Flags().GetBool("silent"); v {
			return LError
		}

		return LInfo
	}

	res, ok := parseLogLevel(levelStr)
	if !ok {
		dief(71, "Invalid value for --log-level flag: %q", levelStr)
	}

	return res
}

func parseLogLevel(s string) (LogLevel, bool) {
	switch s {
	case "error":
		return LError, true
	case "warn":
		return LWarn, true
	case "info":
		return LInfo, true
	case "debug":
		return LDebug, true
	default:
		return 0, false
	}
}

func mustParseHostMappings(vals []string) []hostMapping {
	res, err := parseHostMappings(vals)
	if err != nil {
		dief(71, "Invalid value for --maphost flag: %s", vals)
	}

	return res
}

func parseHostMappings(vals []string) ([]hostMapping, error) {
	var res []hostMapping

	for _, v := range vals {
		// format: srchostname:srcport=dsthostname:dstport
		i := strings.Index(v, "=")
		if i <= 0 {
			return nil, fmt.Errorf("value %q has no separator, =", v)
		}

		srchost, dsthost := v[:i], v[i+1:]
		if j := strings.Index(srchost, ":"); j < 0 {
			return nil, fmt.Errorf("value %q has no src port %q", v, srchost)
		} else if _, err := strconv.Atoi(srchost[j+1:]); err != nil {
			return nil, fmt.Errorf("value %q has invalid src port %q", v, srchost[j+1:])
		} else if k := strings.Index(dsthost, ":"); k > 0 {
			if _, err := strconv.Atoi(dsthost[k+1:]); err != nil {
				return nil, fmt.Errorf("value %q has invalid dst port %q", v, dsthost[k+1:])
			}
		}

		res = append(res, hostMapping{Src: srchost, Dst: dsthost})
	}

	return res, nil
}

func registerAssertionFlags(cmd *cobra.Command) {
	cmd.Flags().Int("assert-status", 0, "Assert response status equals the provided value")
	cmd.Flags().StringArray("assert-header", nil,
		"Assert header matches the provided regexp; NAME alone asserts it is present")
	cmd.Flags().StringArray("assert-header-eq", nil,
		"Assert header equals the provided value; NAME alone asserts it is present")
	cmd.Flags().StringArray("assert-header-missing", nil, "Assert header is missing")
	cmd.Flags().String("assert-body", "", "Assert body matches the provided regexp")
	cmd.Flags().String("assert-body-eq", "", "Assert body equals the provided value")
	cmd.Flags().Bool("assert-body-empty", false, "Assert body is empty")

	// Common shorthands
	cmd.Flags().Bool("assert-ok", false,
		"Assert response status is not an error (2xx or 3xx)")
	cmd.Flags().String("assert-redirect", "",
		"Assert redirect location matches the provided regexp; redirects are not followed")
	cmd.Flags().String("assert-redirect-eq", "",
		"Assert redirect location equals the provided URL; redirects are not followed")
}

// singleValue wraps a flag that holds one value, counting how many times the
// parser assigned to it.
//
// pflag records only whether a flag was set, never how often, so a second
// occurrence of a scalar flag overwrites the first and leaves nothing behind
// to notice. For an assertion that means the run goes green having checked
// less than it was asked to (#35). The count is the missing evidence.
type singleValue struct {
	inner pflag.Value
	count int
}

func (v *singleValue) Set(s string) error { v.count++; return v.inner.Set(s) }
func (v *singleValue) String() string     { return v.inner.String() }
func (v *singleValue) Type() string       { return v.inner.Type() }

// collects reports whether a flag type accumulates its values rather than
// replacing them. Repeating those is how you ask for several assertions.
func collects(flagType string) bool {
	return strings.HasSuffix(flagType, "Array") || strings.HasSuffix(flagType, "Slice")
}

// rejectRepeats prepares every single-valued assertion flag to notice a repeat.
//
// Derived from the flag's own type rather than a list, because a list is how
// this went wrong in the first place: --assert-header was made repeatable and
// the others were not, and nothing connected the two decisions. An assertion
// flag added later is covered the day it is added.
func rejectRepeats(fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		if strings.HasPrefix(f.Name, "assert-") && !collects(f.Value.Type()) {
			f.Value = &singleValue{inner: f.Value}
		}
	})
}

// checkRepeats terminates when an assertion was named more than once. Taking
// the last value silently is the one outcome worth refusing: the alternative
// is a tool that reports success for a check it never ran.
func checkRepeats(fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		if v, ok := f.Value.(*singleValue); ok && v.count > 1 {
			dief(71, "Flag --%s was given %d times but accepts a single value; "+
				"repeat --assert-header, --assert-header-eq or --assert-header-missing "+
				"to make several assertions", f.Name, v.count)
		}
	})
}

// mustCompileAssertion builds a pattern-based assertion, reporting an
// unparseable pattern the way every other invalid flag value is reported.
//
// Without this the pattern reached regexp.MustCompile and the process died with
// a stack trace and exit code 2, which is not part of the documented contract
// and gave the user no idea which flag was at fault (#17).
func mustCompileAssertion(flag, pattern string, build func(string) (Assertion, error)) Assertion {
	a, err := build(pattern)
	if err != nil {
		dief(71, "Invalid value for %s flag: %s", flag, err)
	}

	return a
}

func parseAssertionFlags(cmd *cobra.Command) []Assertion {
	var res []Assertion

	if cmd.Flags().Changed("assert-ok") {
		if v, _ := cmd.Flags().GetBool("assert-ok"); v {
			res = append(res, AssertStatusOK())
		} else {
			res = append(res, AssertStatusNOK())
		}
	}

	if cmd.Flags().Changed("assert-redirect") {
		v, _ := cmd.Flags().GetString("assert-redirect")
		res = append(res, mustCompileAssertion("--assert-redirect", v, AssertRedirectMatch))
	}
	if cmd.Flags().Changed("assert-redirect-eq") {
		v, _ := cmd.Flags().GetString("assert-redirect-eq")
		res = append(res, AssertRedirectEqual(v))
	}

	if cmd.Flags().Changed("assert-status") {
		s, _ := cmd.Flags().GetInt("assert-status")
		res = append(res, AssertStatusEqual(s))
	}

	if cmd.Flags().Changed("assert-header") {
		vs, _ := cmd.Flags().GetStringArray("assert-header")
		res = append(res, parseHeaderAssertions(vs, false)...)
	}
	if cmd.Flags().Changed("assert-header-eq") {
		vs, _ := cmd.Flags().GetStringArray("assert-header-eq")
		res = append(res, parseHeaderAssertions(vs, true)...)
	}
	if cmd.Flags().Changed("assert-header-missing") {
		vs, _ := cmd.Flags().GetStringArray("assert-header-missing")
		for _, v := range vs {
			res = append(res, AssertHeaderMissing(strings.TrimSpace(v)))
		}
	}

	if cmd.Flags().Changed("assert-body") {
		v, _ := cmd.Flags().GetString("assert-body")
		res = append(res, mustCompileAssertion("--assert-body", v, AssertBodyMatch))
	}
	if cmd.Flags().Changed("assert-body-eq") {
		v, _ := cmd.Flags().GetString("assert-body-eq")
		res = append(res, AssertBodyEqual(v))
	}
	if v, _ := cmd.Flags().GetBool("assert-body-empty"); v {
		res = append(res, AssertBodyEmpty())
	}

	return res
}

func parseHeaderAssertions(vs []string, exactMatch bool) []Assertion {
	var res []Assertion

	for _, v := range vs {
		name, value := parseHeaderLine(v)
		if exactMatch {
			if value == "" {
				res = append(res, AssertHeaderPresent(name))
			} else {
				res = append(res, AssertHeaderEqual(name, value))
			}
		} else {
			if value == "" {
				res = append(res, AssertHeaderPresent(name))
			} else {
				res = append(res, mustCompileAssertion("--assert-header", value,
					func(p string) (Assertion, error) { return AssertHeaderMatch(name, p) }))
			}
		}
	}

	return res
}

type Client struct {
	LogLevel      LogLevel
	SkipSslChecks bool
	Timeout       time.Duration
	HostMappings  []hostMapping
	// FollowRedirects turns a 3xx into another request rather than the
	// response the assertions run against.
	FollowRedirects bool
	// MaxRedirects bounds the chain. Zero refuses every redirect; it is only
	// read when FollowRedirects is set.
	MaxRedirects int
	// Retries is how many further attempts may follow a failed one. Zero makes
	// the request once, which is the default and the behaviour that predates
	// retrying.
	Retries int
	// RetryDelay is the fixed wait between attempts. Only read when Retries is
	// positive.
	RetryDelay time.Duration
	// RetryMaxTime bounds the whole run, measured from the first attempt. Zero
	// means only Retries bounds it. It is checked before each retry rather than
	// enforced mid-flight, so an attempt already under way can overrun it by up
	// to one Timeout -- which is curl's meaning for the same option.
	RetryMaxTime time.Duration
}

func (c *Client) Init() {
	// Just print the configuration
	if len(c.HostMappings) > 0 {
		c.logDebug("HostMappings %d:\n", len(c.HostMappings))
		for i := range c.HostMappings {
			c.logDebug("- %q -> %q\n", c.HostMappings[i].Src, c.HostMappings[i].Dst)
		}
	}
}

// errTooManyRedirects marks the one transport-shaped failure this program
// produces itself. Do needs to tell it apart from a network failure, and
// matching on net/http's message text would be a promise net/http never made.
var errTooManyRedirects = errors.New("too many redirects")

// Do makes the request and checks the response against every assertion,
// retrying a failed attempt up to Retries times.
//
// Any failure is retried, an unreachable host and a wrong answer alike. The
// case retrying exists for is waiting for a service to come up, and there the
// response usually arrives perfectly well and says the wrong thing, so a rule
// that retried only transport errors would miss the whole point.
func (c Client) Do(req *http.Request, assertions ...Assertion) error {
	if len(assertions) == 0 {
		// Not a failed attempt but a malformed invocation, so it is reported
		// once rather than retried into the ground.
		return fmt.Errorf("no assertions defined")
	}

	// Built once rather than per attempt: an http.Transport owns an idle
	// connection pool, and a fresh one per attempt would leave --retry 100 of
	// them behind for the lifetime of the process.
	client := c.getHttpClient()
	startedAt := time.Now()

	for attempt := 1; ; attempt++ {
		err := c.doOnce(client, req, assertions)
		if err == nil {
			return nil
		}

		switch {
		case attempt > c.Retries:
			return c.giveUp(attempt, "", err)
		// Checked before sleeping rather than after, so the run ends at the
		// budget instead of one delay past it.
		case c.RetryMaxTime > 0 && time.Since(startedAt)+c.RetryDelay > c.RetryMaxTime:
			return c.giveUp(attempt, "--retry-max-time is "+c.RetryMaxTime.String(), err)
		}

		c.logInfo("[~] retry %d/%d in %s\n", attempt, c.Retries, c.RetryDelay)
		time.Sleep(c.RetryDelay)
	}
}

// giveUp reports the last failure together with how the run ended.
//
// Without the prefix a CI log shows a single failed attempt and no sign that
// five more happened, which reads as a service that was never up rather than
// one that never came up. Nothing is added when nothing was retried, so a plain
// run says exactly what it always said.
func (c Client) giveUp(attempts int, limit string, err error) error {
	if c.Retries <= 0 {
		return err
	}

	if limit != "" {
		limit = " (" + limit + ")"
	}
	return fmt.Errorf("gave up after %d attempts%s:\n%w", attempts, limit, err)
}

// doOnce performs one request and checks it against every assertion.
func (c Client) doOnce(client *http.Client, req *http.Request, assertions []Assertion) error {
	next, err := cloneForAttempt(req)
	if err != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "failed to rewind the request body:\n- %s\n", err)
		c.writeHttpDetails(&b, req, nil)
		return errors.New(b.String())
	}
	req = next

	c.logInfo("[.] %s %s %s", req.Proto, req.Method, req.URL)
	startedAt := time.Now()
	// G704: the request URL comes from the operator's own command line, and
	// fetching it is the entire purpose of this tool -- no trust boundary is
	// crossed, so this is not SSRF.
	//
	// --location widens that slightly: the hops after the first are chosen by
	// the server, not the operator. It stays opt-in for exactly that reason,
	// and net/http drops Authorization and Cookie when a hop leaves the
	// original domain, so credentials passed with -H do not travel.
	res, err := client.Do(req) // #nosec G704 - user asked for this URL
	if err != nil {
		var b strings.Builder
		// The transport did its job here; this program stopped the chain.
		// Filing that under "failed to send request" sends the reader looking
		// for a network problem that does not exist.
		if errors.Is(err, errTooManyRedirects) {
			fmt.Fprintf(&b, "redirect chain was not followed to the end:\n- %s\n", err)
		} else {
			fmt.Fprintf(&b, "failed to send request:\n- %s\n", err)
		}
		// This path logs nothing between [.] and the dump the caller prints,
		// which is fine for a single attempt and unreadable for twenty. The
		// line appears only while retrying so that a plain run is untouched.
		if c.Retries > 0 {
			c.logInfo("[-] FAILED %s: %s\n", time.Since(startedAt), err)
		}
		c.writeHttpDetails(&b, req, nil)
		return errors.New(b.String())
	}
	defer func() { _ = res.Body.Close() }()

	c.logInfo("[:] %s %s\n", res.Proto, res.Status)
	httpRes := &httpResponse{Response: res}
	httpRes.BodyBytes, _ = io.ReadAll(res.Body)

	var assertErrors []error
	for i := range assertions {
		if err := assertions[i](httpRes); err != nil {
			assertErrors = append(assertErrors, err)
		}
	}
	if len(assertErrors) > 0 {
		c.logInfo("[-] FAILED %s\n\n", time.Since(startedAt))

		var b strings.Builder
		fmt.Fprintf(&b, "%d assertions failed:\n", len(assertErrors))
		for i := range assertErrors {
			fmt.Fprintf(&b, "- %s\n", assertErrors[i])
		}
		c.writeHttpDetails(&b, req, httpRes)
		return errors.New(b.String())
	}

	c.logInfo("[+] PASSED %s\n\n", time.Since(startedAt))
	return nil
}

// cloneForAttempt returns a request that can be sent even if the one it was
// built from already has been.
//
// http.Client consumes and closes req.Body, so re-sending the same *http.Request
// carries an empty body unless net/http can replay it through GetBody. Today it
// can -- the CLI builds the body from a strings.Reader, which is one of the
// three types http.NewRequest recognises -- but that is a property of the body
// type rather than a guarantee, and a body without GetBody would silently send
// nothing on the second attempt. Cloning costs six lines and does not depend on
// which reader the caller happened to pass.
func cloneForAttempt(req *http.Request) (*http.Request, error) {
	res := req.Clone(req.Context())
	if req.GetBody == nil {
		return res, nil // no body, or http.NoBody, which re-reads as empty
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	res.Body = body

	return res, nil
}

func (c Client) writeHttpDetails(w io.Writer, req *http.Request, res *httpResponse) {
	_, _ = fmt.Fprintf(w, "\nFAILED: %s %s (%s)\n\n", req.Method, req.URL, req.Proto)
	// With --location the response below came from somewhere else, and the
	// request dumped after this is the one that started the chain rather than
	// the one that produced it. Say so; a reader cannot infer it. The method
	// is worth printing too, because a 302 rewrites POST to GET.
	if res != nil && res.Request != nil && res.Request.URL.String() != req.URL.String() {
		_, _ = fmt.Fprintf(w, "Followed to: %s %s\n\n", res.Request.Method, res.Request.URL)
	}
	_ = req.Write(w)
	_, _ = w.Write([]byte("\n\n"))
	if res != nil {
		res.writeTo(w, c.LogLevel >= LInfo)
		_, _ = w.Write([]byte("\n\n"))
	}
}

func (c Client) getHttpClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 20 * time.Second,
	}

	tr := &http.Transport{
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       20 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		Proxy:                 http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, c.getDstHost(addr))
		},
	}
	if c.SkipSslChecks {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 - user asked for it
	}

	return &http.Client{
		Timeout: c.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !c.FollowRedirects {
				// The default. Hand the 3xx to the assertions intact --
				// --assert-redirect* has nothing to look at otherwise.
				return http.ErrUseLastResponse
			}

			// via holds the requests already sent, so the k-th redirect sees
			// len(via) == k. net/http's own default writes >= here and so
			// follows one fewer hop than its message claims; > is what makes
			// --max-redirs N mean N, and --max-redirs 0 mean none.
			if len(via) > c.MaxRedirects {
				return fmt.Errorf("%w: --max-redirs is %d", errTooManyRedirects, c.MaxRedirects)
			}

			c.logInfo("[>] %d %s %s", len(via), req.Method, req.URL)
			return nil
		},
		Transport: tr,
	}
}

func (c Client) getDstHost(addr string) string {
	for _, r := range c.HostMappings {
		if r.Matches(addr) {
			return r.DstHost()
		}
	}

	return addr
}

func (c Client) logDebug(format string, args ...interface{}) {
	c.log(LDebug, format, args...)
}

func (c Client) logInfo(format string, args ...interface{}) {
	c.log(LInfo, format, args...)
}

func (c Client) log(l LogLevel, format string, args ...interface{}) {
	if l > c.LogLevel {
		return
	}

	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
}

type httpResponse struct {
	*http.Response
	BodyBytes []byte
}

// maxPayloadBytes is how much of a body the failure dump shows before cropping.
const maxPayloadBytes = 256

// writeTo renders the response for a person reading a failure report.
//
// Deliberately not http.Response.Write. That is a wire-format serializer: it
// honours ContentLength and Transfer-Encoding, which describe the body that
// arrived rather than the rendering that replaces it here. A placeholder or a
// hex dump longer than the original body was cut to the original's length, and
// a chunked response had its framing interleaved with the rendering (#18).
//
// Go reported the mismatch on every such run -- "http: ContentLength=4 with
// Body length 26" -- and the caller discarded it. Nothing to discard now: the
// bytes below are the whole output.
//
// Write errors are ignored, following utils.go, because every caller renders
// into an in-memory strings.Builder that cannot fail.
func (r httpResponse) writeTo(w io.Writer, withBody bool) {
	_, _ = fmt.Fprintf(w, "%s %s\n", r.Proto, r.Status)
	writeHeaders(w, r.Header)
	_, _ = fmt.Fprintln(w)

	if !withBody {
		_, _ = fmt.Fprint(w, "  << Payload is omitted >>")
		return
	}
	if cropped := printPayload(w, r.BodyBytes, maxPayloadBytes); cropped > 0 {
		_, _ = fmt.Fprintf(w, "\n\n  << Payload is cropped: %d bytes are hidden >>", cropped)
	}
}

// writeHeaders renders headers one per line, sorted by name so that two dumps
// of the same response can be compared.
func writeHeaders(w io.Writer, h http.Header) {
	for _, name := range slices.Sorted(maps.Keys(h)) {
		for _, value := range h[name] {
			_, _ = fmt.Fprintf(w, "%s: %s\n", name, value)
		}
	}
}

type hostMapping struct {
	// Src is the source host in the form of `hostname:port`.
	Src string
	// Dst is the destination host in the form of either `hostname:port` or just
	// `hostname`. If just the hostname is specified without a port then the
	// source port will be used.
	Dst string
}

func (r hostMapping) Matches(host string) bool {
	if r.Src == "" {
		return false
	}

	if r.Src == "*" || r.Src == "*:*" {
		return true
	}

	if strings.HasPrefix(r.Src, "*:") {
		// Match by port only
		return strings.HasSuffix(host, r.Src[1:])
	}

	return r.Src == host
}

func (r hostMapping) DstHost() string {
	// Dst already has a port
	if idx := strings.Index(r.Dst, ":"); idx >= 0 {
		return r.Dst
	}

	// Use the source port
	var port string
	if idx := strings.Index(r.Src, ":"); idx >= 0 {
		port = r.Src[idx:]
	}
	return r.Dst + port
}
