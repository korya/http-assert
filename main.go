package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {
	cmd := &cobra.Command{
		Use:   "http-assert <URL>",
		Short: "Perform HTTP request and assert received HTTP response",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			c := Client{
				Verbose:       viper.GetBool("verbose"),
				Silent:        viper.GetBool("silent"),
				SkipSslChecks: viper.GetBool("insecure"),
				HostMappings:  mustParseHostMappings(viper.GetStringSlice("maphost")),
			}
			c.Init()

			m, _ := cmd.Flags().GetString("request")
			b := io.Reader(http.NoBody)
			if d, _ := cmd.Flags().GetString("data"); d != "" {
				b = strings.NewReader(d)
			}
			req, err := http.NewRequestWithContext(cmd.Context(), m, args[0], b)
			if err != nil {
				die(91, "Cannot create request '%s %s': %s", m, args[0], err)
			}

			vs, _ := cmd.Flags().GetStringArray("header")
			for _, v := range vs {
				name, value := parseHeaderLine(v)
				req.Header.Add(name, value)
			}
			if err := c.Do(req, parseAssertionFlags(cmd)...); err != nil {
				die(93, "Cannot perform request: %s", err)
			}
		},
	}
	// Deviations from curl's --resolve:
	// - use `=` to separate src and dst
	// - add [:dstport]
	cmd.PersistentFlags().StringArray("maphost", nil,
		"Provide a custom address for a specific host and port pair; "+
			"e.g. <srchostname:srcport=dsthostname[:dstport]>")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Be verbose; log info messages")
	cmd.PersistentFlags().BoolP("silent", "s", false, "Be silent; log errors only")
	cmd.PersistentFlags().BoolP("insecure", "k", false, "Disable checking SSL certificates")
	cmd.Flags().StringP("request", "X", "GET", "Set method for HTTP request")
	cmd.Flags().StringArrayP("header", "H", nil, "Set header for HTTP request")
	cmd.Flags().StringP("data", "d", "",
		"Sends the specified data in a POST request to the HTTP server")
	registerAssertionFlags(cmd)

	_ = viper.BindPFlag("verbose", cmd.PersistentFlags().Lookup("verbose"))
	_ = viper.BindPFlag("silent", cmd.PersistentFlags().Lookup("silent"))
	_ = viper.BindPFlag("insecure", cmd.PersistentFlags().Lookup("insecure"))
	_ = viper.BindPFlag("maphost", cmd.PersistentFlags().Lookup("maphost"))
	viper.SetEnvPrefix("HTTP_ASSERT")
	viper.AutomaticEnv()

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		die(103, "%s", err)
	}
}

func die(rc int, format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, "\nError: "+format, args...)
	os.Exit(rc)
}

func mustParseHostMappings(vals []string) []hostMapping {
	res, err := parseHostMappings(vals)
	if err != nil {
		die(71, "Invalid value for --maphost flag: %s", vals)
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
	cmd.Flags().StringArray("assert-header", nil, "Assert header equals the provided regexp")
	cmd.Flags().StringArray("assert-header-eq", nil, "Assert header equals the provided regexp")
	cmd.Flags().StringArray("assert-header-missing", nil, "Assert header is missing")
	cmd.Flags().String("assert-body", "", "Assert body equals the provided value")
	cmd.Flags().String("assert-body-eq", "", "Assert body equals the provided value")
	cmd.Flags().Bool("assert-body-empty", false, "Assert body is empty")

	// Common shorthands
	cmd.Flags().Bool("assert-ok", false, "Assert response is successful (2xx)")
	cmd.Flags().String("assert-redirect", "", "Assert response redirects to the provided URL")
	cmd.Flags().String("assert-redirect-eq", "", "Assert response redirects to the provided URL")
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
		res = append(res, AssertRedirectMatch(v))
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
		res = append(res, AssertBodyMatch(v))
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
				res = append(res, AssertHeaderMatch(name, value))
			}
		}
	}

	return res
}

type Client struct {
	Verbose       bool
	Silent        bool
	SkipSslChecks bool
	HostMappings  []hostMapping
}

func (c *Client) Init() {
	if c.Verbose {
		c.Silent = false
	}

	// Just print the configuration
	if len(c.HostMappings) > 0 {
		c.logVerbose("HostMappings %d:\n", len(c.HostMappings))
		for i := range c.HostMappings {
			c.logVerbose("- %q -> %q\n", c.HostMappings[i].Src, c.HostMappings[i].Dst)
		}
	}
}

func (c Client) Do(req *http.Request, assertions ...Assertion) error {
	if len(assertions) == 0 {
		return fmt.Errorf("no assertions defined")
	}

	c.logDefault("[.] %s %s %s", req.Proto, req.Method, req.URL)
	startedAt := time.Now()
	res, err := c.getHttpClient().Do(req)
	if err != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "failed to send request:\n- %s\n", err)
		c.writeHttpDetails(&b, req, nil)
		return errors.New(b.String())
	}
	defer res.Body.Close()

	c.logDefault("[:] %s %s\n", res.Proto, res.Status)
	httpRes := &httpResponse{Response: res}
	httpRes.BodyBytes, _ = io.ReadAll(res.Body)

	var assertErrors []error
	for i := range assertions {
		if err := assertions[i](httpRes); err != nil {
			assertErrors = append(assertErrors, err)
		}
	}
	if len(assertErrors) > 0 {
		c.logDefault("[-] FAILED %s\n\n", time.Since(startedAt))

		var b strings.Builder
		fmt.Fprintf(&b, "%d assertions failed:\n", len(assertErrors))
		for i := range assertErrors {
			fmt.Fprintf(&b, "- %s\n", assertErrors[i])
		}
		c.writeHttpDetails(&b, req, httpRes)
		return errors.New(b.String())
	}

	c.logDefault("[+] PASSED %s\n\n", time.Since(startedAt))
	return nil
}

func (c Client) writeHttpDetails(w io.Writer, req *http.Request, res *httpResponse) {
	fmt.Fprintf(w, "\nFAILED: %s %s (%s)\n\n", req.Method, req.URL, req.Proto)
	_ = req.Write(w)
	_, _ = w.Write([]byte("\n\n"))
	if res != nil {
		res.writeTo(w, c.Verbose)
		_, _ = w.Write([]byte("\n\n"))
	}
}

func (c Client) logVerbose(format string, args ...interface{}) {
	if !c.Verbose {
		return
	}

	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
}

func (c Client) logDefault(format string, args ...interface{}) {
	if c.Silent {
		return
	}

	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
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
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &http.Client{
		Timeout: 20 * time.Second,
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

type httpResponse struct {
	*http.Response
	BodyBytes []byte
}

func (r httpResponse) writeTo(w io.Writer, withBody bool) {
	// Ensure to close previous body
	b := r.Response.Body
	defer b.Close()
	if withBody {
		var b bytes.Buffer
		croppedBytes := printPayload(&b, r.BodyBytes, 256)
		if croppedBytes > 0 {
			fmt.Fprintf(&b, "\n\n  << Payload is cropped: %d bytes are hidden >>", croppedBytes)
		}
		r.Response.Body = io.NopCloser(&b)
	} else {
		r.Response.Body = io.NopCloser(strings.NewReader("  << Payload is omitted >>"))
	}
	_ = r.Response.Write(w)
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
