// Command http-assert performs an HTTP request and asserts properties of the
// response, exiting non-zero when an assertion fails.
//
// It is built for the places where a failing exit code is the whole point: a
// pipeline step, a container healthcheck, a monitoring script. Assertions are
// declared as flags, and the request is made once and checked against all of
// them.
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
//	71   a flag or environment value failed to parse
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
		Use:     "http-assert <URL>",
		Short:   "Perform HTTP request and assert received HTTP response",
		Version: versionString(),
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			insecure, _ := cmd.Flags().GetBool("insecure")
			maxTime, _ := cmd.Flags().GetInt("max-time")
			maphost, _ := cmd.Flags().GetStringArray("maphost")
			c := Client{
				LogLevel:      mustParseLogLevel(cmd),
				SkipSslChecks: insecure,
				Timeout:       time.Duration(maxTime) * time.Second,
				HostMappings:  mustParseHostMappings(maphost),
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
	registerAssertionFlags(cmd)

	cmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		applyEnv(cmd.Flags())
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
		"Assert redirect location matches the provided regexp")
	cmd.Flags().String("assert-redirect-eq", "",
		"Assert redirect location equals the provided URL")
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

func (c Client) Do(req *http.Request, assertions ...Assertion) error {
	if len(assertions) == 0 {
		return fmt.Errorf("no assertions defined")
	}

	c.logInfo("[.] %s %s %s", req.Proto, req.Method, req.URL)
	startedAt := time.Now()
	// G704: the request URL comes from the operator's own command line, and
	// fetching it is the entire purpose of this tool -- no trust boundary is
	// crossed, so this is not SSRF.
	res, err := c.getHttpClient().Do(req) // #nosec G704 - user asked for this URL
	if err != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "failed to send request:\n- %s\n", err)
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

func (c Client) writeHttpDetails(w io.Writer, req *http.Request, res *httpResponse) {
	_, _ = fmt.Fprintf(w, "\nFAILED: %s %s (%s)\n\n", req.Method, req.URL, req.Proto)
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
			return http.ErrUseLastResponse // Disallow redirects
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
