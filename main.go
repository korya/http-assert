// Command http-assert performs an HTTP request and asserts properties of the
// response, exiting non-zero when an assertion fails.
//
// It is built for the places where a failing exit code is the whole point: a
// pipeline step, a container healthcheck, a monitoring script. Assertions are
// declared as flags, and the request is made once and checked against all of
// them.
//
// The three header assertions and --assert-jq can be repeated to assert
// several things at once. Every other assertion flag takes a single value and
// is rejected if given twice, rather than quietly keeping the last one.
//
// The two boolean assertions negate with =false, which selects the opposite
// assertion rather than cancelling the flag.
//
// --assert-jq asserts a jq expression against a JSON body, and repeats like the
// header assertions do. The expression yields the verdict itself, so there is
// no path-and-value syntax and no question of whether 5 means the number or the
// string -- jq already has types, comparison and regexp.
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
//	71   the invocation was rejected; no request was attempted
//	92   the request produced no usable response
//	93   a response arrived, and at least one assertion failed
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
//
// # Compression
//
// A compressed body is decoded before the assertions run, so --assert-body and
// friends always see the payload. gzip and deflate are understood; an encoding
// nothing here can remove fails the body assertions by name and leaves every
// other assertion alone.
//
// The request advertises no Accept-Encoding of its own, and the response
// headers are reported exactly as they arrived. net/http would decode only
// what it had asked for itself and would delete Content-Encoding and
// Content-Length on the way, which made a response that was compressed
// indistinguishable from one that never was.
package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/json"
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

Repeat --assert-header, --assert-header-eq, --assert-header-missing or
--assert-jq to make several assertions of that kind. Every other assertion flag
takes a single value and is rejected if given twice, rather than quietly
keeping the last.

The two boolean assertions can be negated with =false, which selects the
opposite assertion rather than cancelling the flag: --assert-ok=false asserts
the status IS an error, and --assert-body-empty=false asserts the body is not
empty.

Repeat --assert-jq to assert several jq expressions against a JSON body. Each
must yield true; a query that yields nothing has checked nothing and fails.
A query is compiled before the request is made, so a broken one exits 71.

Exit codes:
  0    every assertion passed
  71   the invocation was rejected; no request was attempted
  92   the request produced no usable response
  93   a response arrived, and at least one assertion failed

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

  --assert-ok counts a 3xx as success, so a redirecting endpoint passes a
  health check without the resource behind it ever being fetched.

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
  Both --retry-delay and --retry-max-time need --retry to mean anything.

Compression:
  A compressed body is decoded before the assertions run, so --assert-body and
  the other body assertions always see the payload. gzip and deflate are
  understood; an encoding with no decoder here fails the body assertions by
  name and leaves --assert-ok, --assert-status and --assert-header* alone.

  Nothing is advertised in Accept-Encoding unless -H says so, and the response
  headers are reported exactly as they arrived -- so a body can be asserted on
  and its Content-Encoding at the same time.`,
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
		// Errors exit through dief with one line and the invocation code;
		// cobra's own reporting would add a 40-line usage dump that buries
		// the error and print it twice. --help is unaffected: cobra renders
		// help before the silenced error path is reached.
		SilenceErrors: true,
		SilenceUsage:  true,
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

			// -d implies POST, as it does in curl; an explicit -X wins even
			// when it repeats the default, which Changed distinguishes from
			// "not passed" -- applyEnv cannot fake it because neither flag is
			// env-applied and Value.Set does not mark Changed.
			m, _ := cmd.Flags().GetString("request")
			dataGiven := cmd.Flags().Changed("data")
			if dataGiven && !cmd.Flags().Changed("request") {
				m = http.MethodPost
			}
			b := io.Reader(http.NoBody)
			if dataGiven {
				d, _ := cmd.Flags().GetString("data")
				b = strings.NewReader(d)
			}
			assertions := parseAssertionFlags(cmd)
			if len(assertions) == 0 {
				dief(exitBadInvocation, "No assertions specified; pass at "+
					"least one --assert-* flag (e.g. --assert-ok)")
			}

			req, err := http.NewRequestWithContext(cmd.Context(), m, args[0], b)
			if err != nil {
				dief(exitBadInvocation, "Cannot create request '%s %s': %s", m, args[0], err)
			}

			vs, _ := cmd.Flags().GetStringArray("header")
			for _, v := range vs {
				name, value := mustParseRequestHeader(v)
				req.Header.Add(name, value)
			}
			// curl's default for -d; presence is what suppresses it, so an
			// explicit empty `-H 'Content-Type:'` is respected, not replaced.
			if dataGiven && len(req.Header.Values("Content-Type")) == 0 {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if err := c.Do(req, assertions...); err != nil {
				// An error nobody tagged stays in the transport bucket: wrong
				// by at most one category, and never reported as a usage
				// mistake the caller did not make.
				e := &exitError{code: exitTransportFail}
				_ = errors.As(err, &e)
				if e.code == exitAssertFail {
					// The assertion dump names itself; a transport-flavoured
					// prefix would send the reader to the network.
					dief(exitAssertFail, "%s", err)
				}
				dief(e.code, "Cannot perform request: %s", err)
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
		"Be verbose; log debug messages (same as --log-level debug; overrides --log-level)")
	cmd.PersistentFlags().BoolP("silent", "s", false,
		"Be silent; log error messages only (same as --log-level error; overrides -v)")
	cmd.PersistentFlags().String("log-level", "",
		"Set log level; possible values: debug, info (default), warn, error")
	cmd.PersistentFlags().BoolP("insecure", "k", false, "Disable checking SSL certificates")
	cmd.PersistentFlags().IntP("max-time", "m", 20,
		"Maximum time in seconds that you allow each request to take")
	cmd.Flags().StringP("request", "X", "GET",
		"Set method for HTTP request; overrides the POST that -d implies")
	cmd.Flags().StringArrayP("header", "H", nil,
		"Set header for HTTP request, as <name: value>; a name alone is rejected")
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
		// Arg-count, unknown-flag and unparseable-value errors all land here,
		// so a typo on the command line and the same typo in the environment
		// finally exit with the same code.
		dief(exitBadInvocation, "%s; run 'http-assert --help' for usage", err)
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
			dief(exitBadInvocation, "Invalid value for %s=%q: %s", key, v, err)
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
				dief(exitBadInvocation, "Flags --location and --%s cannot be used together: "+
					"--location follows the redirect, which consumes the 3xx "+
					"response --%s inspects", name, name)
			}
		}
	}

	if !fs.Changed("max-redirs") {
		return
	}
	if !follow {
		dief(exitBadInvocation, "Flag --max-redirs bounds a redirect chain that is not being "+
			"followed; pass --location, or drop --max-redirs")
	}
	if n, _ := fs.GetInt("max-redirs"); n < 0 {
		dief(exitBadInvocation, "Invalid value for --max-redirs flag: %d; it counts redirects, "+
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
		dief(exitBadInvocation, "Invalid value for --retry flag: %d; it counts retries, so the "+
			"smallest meaningful value is 0", n)
	}

	for _, name := range []string{"retry-delay", "retry-max-time"} {
		if !fs.Changed(name) {
			continue
		}
		if !fs.Changed("retry") {
			dief(exitBadInvocation, "Flag --%s configures retrying that is not switched on; "+
				"pass --retry, or drop --%s", name, name)
		}
		if d, _ := fs.GetDuration(name); d < 0 {
			dief(exitBadInvocation, "Invalid value for --%s flag: %s; it is a length of time, "+
				"so the smallest meaningful value is 0", name, d)
		}
	}
}

// Exit codes. The code answers whose fault the failure is: the invocation
// (fix the command line), the transport (no usable response arrived), or the
// response (it arrived and said the wrong thing).
//
// The e2e harness keeps its own copy of these values because it tests the
// built binary from the outside; the help test keeps both in step with the
// documentation.
const (
	exitOK            = 0
	exitBadInvocation = 71 // the request was never attempted; fix the command line
	exitTransportFail = 92 // the request produced no usable response
	exitAssertFail    = 93 // a response arrived and at least one assertion failed
)

// exitError carries the exit category from the place a failure is understood
// -- inside the attempt, where transport and assertion failures are told
// apart -- to the place the process exits. It survives the retry wrapper
// because giveUp wraps with %w and errors.As unwraps it.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

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

// levelRequest is one verbosity option asking for a level, named the way the
// caller spelled it so a conflict warning can point at the right surface.
type levelRequest struct {
	name  string
	level LogLevel
}

// levelRequests returns the options that ask for a log level on one channel:
// the command line when fromCLI, the environment otherwise. Changed tells the
// channels apart -- parsing sets it, applyEnv's Value.Set does not. The slice
// is in priority order, -s before -v before --log-level, so the first entry
// is the channel's winner. A false boolean or an empty --log-level declines
// to ask rather than asking for the default, which lets -v=false cancel an
// environment-supplied verbose without starting a conflict.
func levelRequests(fs *pflag.FlagSet, fromCLI bool) []levelRequest {
	name := func(flag, env string) string {
		if fromCLI {
			return flag
		}
		return env
	}

	var res []levelRequest
	if fs.Changed("silent") == fromCLI {
		if v, _ := fs.GetBool("silent"); v {
			res = append(res, levelRequest{name("-s", "HTTP_ASSERT_SILENT"), LError})
		}
	}
	if fs.Changed("verbose") == fromCLI {
		if v, _ := fs.GetBool("verbose"); v {
			res = append(res, levelRequest{name("-v", "HTTP_ASSERT_VERBOSE"), LDebug})
		}
	}
	if fs.Changed("log-level") == fromCLI {
		if s, _ := fs.GetString("log-level"); s != "" {
			lv, ok := parseLogLevel(s)
			if !ok {
				dief(exitBadInvocation, "Invalid value for --log-level flag: %q", s)
			}
			res = append(res, levelRequest{name("--log-level "+s, "HTTP_ASSERT_LOG_LEVEL"), lv})
		}
	}
	return res
}

// mustParseLogLevel resolves the three verbosity options into one level: the
// command line beats the environment as a whole, and within one channel -s
// beats -v beats --log-level. A conflict never fails the run -- verbosity is
// not worth aborting over -- but every overridden request that asked for a
// different level is announced (#29).
//
// The announcement goes straight to stderr rather than through the logger:
// a warn-severity log line would be suppressed by the very level it reports,
// so `-v -s` would resolve to silent and swallow its own explanation.
func mustParseLogLevel(cmd *cobra.Command) LogLevel {
	fs := cmd.Flags()
	// The command-line requests come first, so the head of the combined list
	// is the overall winner and the environment's requests can only lead when
	// no command-line option asked at all. Keeping the losing channel in the
	// list is what makes a cross-channel override visible in the warning.
	reqs := append(levelRequests(fs, true), levelRequests(fs, false)...)
	if len(reqs) == 0 {
		return LInfo
	}

	winner := reqs[0]
	for _, loser := range reqs[1:] {
		if loser.level != winner.level {
			fmt.Fprintf(os.Stderr, "Warning: %s overrides %s\n", winner.name, loser.name)
		}
	}
	return winner.level
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

// mustParseRequestHeader parses a -H value, refusing the two forms that would
// put a header on the wire the caller did not describe.
//
// parseHeaderLine is shared with --assert-header*, where a name on its own is
// meaningful: it asserts the header is present. A request has no such reading.
// A name with no colon was sent as a header with an empty value, which is the
// opposite of what the same input means to curl -- there it removes an
// internally-generated header -- so a user reaching for that idiom got the one
// outcome they were trying to avoid, silently (#33).
//
// The validation lives here rather than in the parser for that reason: the
// parser is right for one caller and wrong for the other.
func mustParseRequestHeader(v string) (name, value string) {
	if !strings.Contains(v, ":") {
		dief(exitBadInvocation, "Invalid value for --header flag: %q has no ':' separator; "+
			"write %q to send the header with an empty value", v, v+":")
	}

	name, value = parseHeaderLine(v)
	if name == "" {
		dief(exitBadInvocation, "Invalid value for --header flag: %q has no header name "+
			"before the ':'", v)
	}

	return name, value
}

func mustParseHostMappings(vals []string) []hostMapping {
	res, err := parseHostMappings(vals)
	if err != nil {
		dief(exitBadInvocation, "Invalid value for --maphost flag: %s", vals)
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
	cmd.Flags().Bool("assert-body-empty", false,
		"Assert body is empty; =false asserts it is not")
	cmd.Flags().StringArray("assert-jq", nil,
		"Assert the jq expression yields true; repeat to assert several")

	// Common shorthands
	cmd.Flags().Bool("assert-ok", false,
		"Assert response status is not an error (2xx or 3xx); =false asserts it is")
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
			dief(exitBadInvocation, "Flag --%s was given %d times but accepts a single value; "+
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
		dief(exitBadInvocation, "Invalid value for %s flag: %s", flag, err)
	}

	return a
}

// boolAssertion turns a boolean assertion flag into the assertion it asks for.
//
// A boolean assertion has two of them, and =false selects the second rather
// than cancelling the flag: --assert-ok asserts the status is not an error,
// --assert-ok=false asserts that it is. Naming an assertion and getting no
// assertion would be the one outcome worth refusing, and it is what
// --assert-body-empty=false used to do -- the run died with "no assertions
// defined" after the user had named one (#32).
//
// The pairing lives here, once, rather than being written out per flag. The two
// flags drifted apart because nothing connected them; a helper is what connects
// them, in the same way rejectRepeats derives from the flag's type rather than
// from a list.
func boolAssertion(cmd *cobra.Command, name string, whenTrue, whenFalse func() Assertion) []Assertion {
	if !cmd.Flags().Changed(name) {
		return nil
	}

	if v, _ := cmd.Flags().GetBool(name); v {
		return []Assertion{whenTrue()}
	}

	return []Assertion{whenFalse()}
}

func parseAssertionFlags(cmd *cobra.Command) []Assertion {
	var res []Assertion

	res = append(res, boolAssertion(cmd, "assert-ok", AssertStatusOK, AssertStatusNOK)...)
	res = append(res, boolAssertion(cmd, "assert-body-empty", AssertBodyEmpty, AssertBodyNotEmpty)...)

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

	// One assertion per occurrence. --assert-jq is a stringArray, so rejectRepeats
	// leaves it alone and repeating it accumulates, exactly as it does for the
	// three header assertions.
	if cmd.Flags().Changed("assert-jq") {
		vs, _ := cmd.Flags().GetStringArray("assert-jq")
		for _, v := range vs {
			res = append(res, mustCompileAssertion("--assert-jq", v, AssertJQ))
		}
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
		// once rather than retried into the ground. The CLI checks this before
		// calling; the guard backstops any other caller.
		return &exitError{exitBadInvocation, "no assertions defined"}
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
		return &exitError{exitTransportFail, b.String()}
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
		return &exitError{exitTransportFail, b.String()}
	}
	defer func() { _ = res.Body.Close() }()

	c.logInfo("[:] %s %s\n", res.Proto, res.Status)
	httpRes := &httpResponse{Response: res}
	httpRes.BodyBytes, _ = io.ReadAll(res.Body)
	httpRes.decodeBody()

	var assertErrors []error
	for i := range assertions {
		// A failed assertion and one that could not be evaluated are both
		// failures of the run and both print the same way, so they share a
		// list -- which is also what keeps the dump in the order the
		// assertions were given. Only a machine-readable consumer needs to
		// tell them apart, and that is what Check separates them for (#45).
		f, err := assertions[i].Check(httpRes)
		switch {
		case err != nil:
			assertErrors = append(assertErrors, err)
		case f != nil:
			assertErrors = append(assertErrors, f)
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
		return &exitError{exitAssertFail, b.String()}
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
	writeRequest(w, req)
	_, _ = w.Write([]byte("\n\n"))
	if res != nil {
		res.writeTo(w, c.LogLevel >= LInfo)
		_, _ = w.Write([]byte("\n\n"))
	}
}

// writeRequest renders the request for a person reading a failure report, the
// way writeTo renders the response.
//
// http.Request.Write alone gets this wrong twice. Its body has already been
// consumed by the send, so it emits headers claiming a Content-Length with
// nothing behind them -- the first question after a failed POST is "what did I
// send?", and that was the one thing the dump left out (#19). And it is a
// wire-format serializer, so a long or binary payload would land in the report
// raw: the same mistake #18 fixed on the response side.
//
// So the headers come from Write, which knows what actually goes on the wire
// (Host, User-Agent, Content-Length are none of them in req.Header), and the
// body goes through the shared renderer that crops and hex-dumps.
func writeRequest(w io.Writer, req *http.Request) {
	// A fresh clone replays the body; without GetBody there is nothing to
	// replay and the dump is no worse than it was.
	dump := req
	if fresh, err := cloneForAttempt(req); err == nil {
		dump = fresh
	}

	var b bytes.Buffer
	if err := dump.Write(&b); err != nil {
		// A failed dump must not replace the failure being reported, so
		// whatever was rendered before the error still goes out.
		_, _ = w.Write(b.Bytes())
		return
	}

	head, body, found := bytes.Cut(b.Bytes(), []byte("\r\n\r\n"))
	_, _ = w.Write(head)
	_, _ = w.Write([]byte("\r\n\r\n"))
	if !found || len(body) == 0 {
		return
	}

	if cropped := printPayload(w, body, maxPayloadBytes); cropped > 0 {
		_, _ = fmt.Fprintf(w, "\n\n  << Payload is cropped: %d bytes are hidden >>", cropped)
	}
}

func (c Client) getHttpClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 20 * time.Second,
	}

	tr := &http.Transport{
		ForceAttemptHTTP2: false,
		// net/http decodes a gzip response only when it was the layer that
		// asked for it, and hands over the raw bytes otherwise. Four unrelated
		// conditions decide which happens -- a caller-set Accept-Encoding, a
		// Range header, the method, and this field -- so whether --assert-body
		// saw the payload or a compressed blob depended on flags that have
		// nothing to do with the body (#27).
		//
		// Decoding is done here instead, in decodeBody, on every response. One
		// path, and the request carries exactly the headers it was told to.
		DisableCompression:    true,
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
	// Encoding is the response's Content-Encoding, verbatim, and empty when
	// there was none.
	//
	// The header itself is left alone. net/http deletes it (and Content-Length)
	// when it decodes, which makes a response that was compressed
	// indistinguishable from one that never was -- and the whole reason to set
	// Accept-Encoding by hand is to find out which happened.
	Encoding string
	// DecodeErr is why BodyBytes is still encoded. Nil means BodyBytes is the
	// payload, whether or not anything had to be removed to get there.
	DecodeErr error
	// The decoded JSON body, filled by decodeJSON on first use. Plain fields
	// rather than a sync.Once because httpResponse is passed around by value in
	// places, and a value copy of a mutex is what go vet exists to catch.
	jsonBody   any
	jsonErr    error
	jsonParsed bool
}

// decodeJSON decodes the body as JSON once and shares the result.
//
// Every --assert-jq in a run reads the same response, so ten queries should
// parse it once rather than ten times. Failure is reported as a property of the
// body, not of the query: a response that is not JSON fails every jq assertion
// for the same reason, and saying so once per assertion is clearer than saying
// the expression did not hold.
func (r *httpResponse) decodeJSON() (any, error) {
	if r.jsonParsed {
		return r.jsonBody, r.jsonErr
	}
	r.jsonParsed = true

	// Through bodyOf like every other body assertion, so a body that is still
	// compressed reports that rather than reporting invalid JSON (#27).
	body, err := bodyOf(r)
	if err != nil {
		r.jsonErr = err
		return nil, r.jsonErr
	}

	if err := json.Unmarshal(body, &r.jsonBody); err != nil {
		r.jsonErr = fmt.Errorf("body: expected JSON, got %s", err)
		return nil, r.jsonErr
	}

	return r.jsonBody, nil
}

// decoders maps a Content-Encoding to something that removes it. Content
// coding names are case-insensitive per RFC 9110, so lookups are lowered.
//
// deflate is absent by name because it is two formats: RFC 9110 says zlib, and
// a good deal of the web sends raw. decodeDeflate tries both.
var decoders = map[string]func([]byte) ([]byte, error){
	"gzip":    decodeGzip,
	"deflate": decodeDeflate,
}

// decodeBody removes the Content-Encoding from BodyBytes, leaving every header
// exactly as it arrived.
//
// An encoding nothing here can remove is not an error by itself: a response
// body the tool cannot read still has a status and headers worth asserting on.
// It is recorded instead, and only the body assertions refuse (see bodyOf).
func (r *httpResponse) decodeBody() {
	r.Encoding = strings.TrimSpace(r.Header.Get("Content-Encoding"))

	// An empty body has nothing to decode, and an empty gzip stream is an error
	// rather than an empty payload -- so a 204 that carries the header anyway
	// must not fail --assert-body-empty.
	if len(r.BodyBytes) == 0 {
		return
	}

	switch enc := strings.ToLower(r.Encoding); enc {
	case "", "identity":
		return
	default:
		decode, ok := decoders[enc]
		if !ok {
			r.DecodeErr = fmt.Errorf("no decoder for %q; gzip and deflate are supported", r.Encoding)
			return
		}

		b, err := decode(r.BodyBytes)
		if err != nil {
			r.DecodeErr = err
			return
		}
		r.BodyBytes = b
	}
}

func decodeGzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()

	return io.ReadAll(zr)
}

// decodeDeflate tries zlib first and raw DEFLATE second.
//
// RFC 9110 defines the deflate coding as the zlib format, but servers sending
// raw DEFLATE under the same name are common enough that net/http declines to
// negotiate it at all ("Deflate is ambiguous and not as universally supported
// anyway", transport.go). Guessing is safe here because neither reader accepts
// the other's input: a wrong guess fails rather than producing plausible bytes.
func decodeDeflate(b []byte) ([]byte, error) {
	if zr, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
		defer func() { _ = zr.Close() }()
		if out, err := io.ReadAll(zr); err == nil {
			return out, nil
		}
	}

	fr := flate.NewReader(bytes.NewReader(b))
	defer func() { _ = fr.Close() }()

	out, err := io.ReadAll(fr)
	if err != nil {
		return nil, fmt.Errorf("not valid zlib or raw DEFLATE: %w", err)
	}

	return out, nil
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
	// The headers above still say how the body arrived, so plain text under a
	// Content-Encoding header needs explaining -- as does a hex dump under one.
	switch {
	case r.DecodeErr != nil:
		_, _ = fmt.Fprintf(w, "  << Payload is %s-encoded and was not decoded >>\n\n", r.Encoding)
	case r.Encoding != "" && !strings.EqualFold(r.Encoding, "identity"):
		_, _ = fmt.Fprintf(w, "  << Payload decoded from %s >>\n\n", r.Encoding)
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
