package main

import (
	"bytes"
	"context"
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
				Verbose:      viper.GetBool("verbose"),
				HostMappings: mustParseHostMappings(viper.GetStringSlice("maphost")),
			}

			m, _ := cmd.Flags().GetString("request")
			b := io.Reader(http.NoBody)
			if d, _ := cmd.Flags().GetString("data"); d != "" {
				b = strings.NewReader(d)
			}
			req, err := http.NewRequestWithContext(cmd.Context(), m, args[0], b)
			if err != nil {
				die(91, "Cannot create %s request: %s", m, err)
			}

			if err := c.Do(req, parseAssertionFlags(cmd)...); err != nil {
				die(93, "Cannot create %s request: %s", m, err)
			}
		},
	}
	// Deviations from curl's --resolve:
	// - use `=` to separate src and dst
	// - add [:dstport]
	cmd.PersistentFlags().StringArray("maphost", nil,
		"Provide a custom address for a specific host and port pair; "+
			"e.g. <srchostname:srcport=dsthostname[:dstport]...>")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Be verbose")
	cmd.Flags().StringP("request", "X", "GET",
		"Specifies a custom request method to use when communicating with the HTTP server")
	cmd.Flags().StringP("data", "d", "",
		"Sends the specified data in a POST request to the HTTP server")
	registerAssertionFlags(cmd)

	viper.BindPFlag("verbose", cmd.PersistentFlags().Lookup("verbose"))
	viper.BindPFlag("maphost", cmd.PersistentFlags().Lookup("maphost"))
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
	fmt.Fprintf(os.Stderr, "Error: "+format, args...)
	os.Exit(rc)
}

func mustParseHostMappings(vals []string) []hostMapping {
	res, err := parseHostMappings(vals)
	if err != nil {
		die(91, "Invalid value for --maphost flag: %s")
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
	cmd.Flags().StringP("assert-body", "B", "", "Assert body equals the provided value")

	// Common shorthands
	cmd.Flags().Bool("assert-ok", false, "Assert response is successful (2xx)")
	cmd.Flags().String("assert-redirect", "", "Assert response redirects to the provided URL")
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
		if strings.HasPrefix(v, "=") {
			res = append(res, AssertRedirectEqual(v[1:]))
		} else {
			res = append(res, AssertRedirectMatch(v))
		}
	}

	if cmd.Flags().Changed("assert-status") {
		s, _ := cmd.Flags().GetInt("assert-status")
		res = append(res, AssertStatusEqual(s))
	}

	hs, _ := cmd.Flags().GetStringArray("assert-header")
	for _, h := range hs {
		parts := strings.SplitN(h, ":", 2)
		name := strings.TrimSpace(parts[0])
		var value string
		if len(parts) > 1 {
			value = strings.TrimSpace(parts[1])
		}
		var exactMatch bool
		if strings.HasPrefix(name, "=") {
			name = name[1:]
			exactMatch = true
		}

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

	if cmd.Flags().Changed("assert-body") {
		v, _ := cmd.Flags().GetString("assert-body")
		if strings.HasPrefix(v, "=") {
			res = append(res, AssertBodyEqual(v[1:]))
		} else {
			res = append(res, AssertBodyMatch(v))
		}
	}

	return res
}

type Client struct {
	Verbose      bool
	HostMappings []hostMapping
}

func (c Client) Do(req *http.Request, assertions ...Assertion) error {
	if len(assertions) == 0 {
		return fmt.Errorf("no assertions defined")
	}

	if c.Verbose {
		c.log("Performing: '%s %s'", req.Method, req.URL)
		if len(c.HostMappings) > 0 {
			c.log("HostMappings %d:\n", len(c.HostMappings))
			for i := range c.HostMappings {
				c.log("- %q -> %q\n", c.HostMappings[i].Src, c.HostMappings[i].Dst)
			}
		}
	}

	res, err := c.getHttpClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

	if c.Verbose {
		c.log("Get: %d (%s)\n", res.StatusCode, res.Status)
	}

	httpRes := &httpResponse{Response: res}
	httpRes.BodyBytes, _ = io.ReadAll(res.Body)

	var assertErrors []error
	for i := range assertions {
		if err := assertions[i](httpRes); err != nil {
			assertErrors = append(assertErrors, err)
		}
	}
	if len(assertErrors) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%d assertions failed:\n", len(assertErrors))
		for i := range assertErrors {
			fmt.Fprintf(&b, "- %s\n", assertErrors[i])
		}
		b.WriteString("\n\n")
		httpRes.writeTo(&b, c.Verbose)
		b.WriteString("\n")
		return errors.New(b.String())
	}

	return nil
}

func (c Client) log(format string, args ...interface{}) {
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

	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Disallow redirects
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          10,
			IdleConnTimeout:       20 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			Proxy:                 http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, c.getDstHost(addr))
			},
		},
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
		r.Response.Body = io.NopCloser(bytes.NewReader(r.BodyBytes))
	} else {
		r.Response.Body = io.NopCloser(strings.NewReader("<<Payload is omitted>>"))
	}
	r.Response.Write(w)
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
	return r.Src != "" && r.Src == host
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
