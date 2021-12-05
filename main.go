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
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// - Support curl's --resolve flag
// - Support curl's -f flag
// - Send HTTP request
// - Assert: status, headers and body
// resolves = []resolvePair{
// 	{Host: "youriguide.com:123", Addr: "youriguide.com:443"},
// 	{Host: "youriguide.com:901", Addr: "youriguide.com:80"},
// }

// mustPass(AssertRequest("https://youriguide.com", AssertStatusOK()))
// mustPass(AssertRequest("https://youriguide.com:124"))
// mustPass(AssertRequest("http://youriguide.com"))
// mustPass(AssertRequest("http://youriguide.com:901"))

func main() {
	rootCmd := &cobra.Command{
		Use:   "http-assert",
		Short: "Perform HTTP requests and assert received responses",
	}
	rootCmd.PersistentFlags().StringArray("resolve", nil,
		"Provide a custom address for a specific host and port pair; e.g. <host:port:addr[,addr]...>")

	cmdCurl := &cobra.Command{
		Use:   "do <URL>",
		Short: "Perform an HTTP request",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			httpClient := getHttpClient(parseResolves(cmd))

			m, _ := cmd.Flags().GetString("request")
			b := io.Reader(http.NoBody)
			if d, _ := cmd.Flags().GetString("data"); d != "" {
				b = strings.NewReader(d)
			}
			req, err := http.NewRequestWithContext(cmd.Context(), m, args[0], b)
			if err != nil {
				die(3, "Cannot create %s request: %s", m, err)
			}

			mustPass(assertRequest(httpClient, req, parseAssertionFlags(cmd)...))
		},
	}
	rootCmd.AddCommand(cmdCurl)
	registerAssertionFlags(cmdCurl)
	cmdCurl.Flags().StringP("request", "X", "GET",
		"Specifies a custom request method to use when communicating with the HTTP server")
	cmdCurl.Flags().StringP("data", "d", "",
		"Sends the specified data in a POST request to the HTTP server")

	cmdGet := &cobra.Command{
		Use:   "GET <URL>",
		Short: "Perform an HTTP GET request (shortcut for `do -X GET`)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			httpClient := getHttpClient(parseResolves(cmd))

			req, err := http.NewRequestWithContext(cmd.Context(),
				http.MethodGet, args[0], http.NoBody)
			if err != nil {
				die(3, "Cannot create GET request: %s")
			}

			mustPass(assertRequest(httpClient, req, parseAssertionFlags(cmd)...))
		},
	}
	rootCmd.AddCommand(cmdGet)
	registerAssertionFlags(cmdGet)

	cmdPut := &cobra.Command{
		Use:   "PUT <URL>",
		Short: "Perform an HTTP PUT request (shortcut for `do -X PUT`)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			httpClient := getHttpClient(parseResolves(cmd))

			req, err := http.NewRequestWithContext(cmd.Context(),
				http.MethodPut, args[0], http.NoBody)
			if err != nil {
				die(3, "Cannot create GET request: %s")
			}

			mustPass(assertRequest(httpClient, req, parseAssertionFlags(cmd)...))
		},
	}
	rootCmd.AddCommand(cmdPut)
	registerAssertionFlags(cmdPut)

	cmdPost := &cobra.Command{
		Use:   "POST <URL>",
		Short: "Perform an HTTP POST request (shortcut for `do -X POST`)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			httpClient := getHttpClient(parseResolves(cmd))

			req, err := http.NewRequestWithContext(cmd.Context(),
				http.MethodPost, args[0], http.NoBody)
			if err != nil {
				die(3, "Cannot create GET request: %s")
			}

			mustPass(assertRequest(httpClient, req, parseAssertionFlags(cmd)...))
		},
	}
	rootCmd.AddCommand(cmdPost)
	registerAssertionFlags(cmdPost)

	cmdDelete := &cobra.Command{
		Use:   "DELETE <URL>",
		Short: "Perform an HTTP DELETE request (shortcut for `do -X DELETE`)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			httpClient := getHttpClient(parseResolves(cmd))

			req, err := http.NewRequestWithContext(cmd.Context(),
				http.MethodDelete, args[0], http.NoBody)
			if err != nil {
				die(3, "Cannot create GET request: %s")
			}

			mustPass(assertRequest(httpClient, req, parseAssertionFlags(cmd)...))
		},
	}
	rootCmd.AddCommand(cmdDelete)
	registerAssertionFlags(cmdDelete)

	if err := rootCmd.Execute(); err != nil {
		die(99, "%s", err)
	}
}

func die(rc int, format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, "Error: "+format, args...)
	os.Exit(rc)
}

func mustPass(err error) {
	if err != nil {
		die(101, "%s", err)
	}
}

func parseResolves(cmd *cobra.Command) []resolvePair {
	var res []resolvePair

	rs, _ := cmd.Flags().GetStringArray("resolve")
	for _, r := range rs {
		// format: host:port:addr
		firstColon := strings.Index(r, ":")
		if firstColon < 0 {
			die(91, "Invalid value for --resolve flag: %q", r)
		}

		secondColon := strings.Index(r[firstColon+1:], ":")
		if secondColon < 0 {
			die(91, "Invalid value for --resolve flag: %q", r)
		}

		res = append(res, resolvePair{Host: r[:secondColon], Addr: r[secondColon+1:]})
	}

	return res
}

func registerAssertionFlags(cmd *cobra.Command) {
	cmd.Flags().Int("status", 0, "Assert response status equals the provided value")
	cmd.Flags().StringArrayP("header", "H", nil, "Assert header equals the provided regexp")
	cmd.Flags().StringArrayP("header-match", "M", nil, "Assert header matches the provided regexp")
	cmd.Flags().StringP("body", "B", "", "Assert body equals the provided value")
	cmd.Flags().String("body-match", "", "Assert body matches the provided regexp")

	// Common shorthands
	cmd.Flags().Bool("ok", false, "Assert response is successful (2xx)")
	cmd.Flags().String("redirect-to", "", "Assert response redirects to the provided URL")
	cmd.Flags().String("redirect-match", "", "Assert response redirects to URL macthing the provided regexp")
}

func parseAssertionFlags(cmd *cobra.Command) []Assertion {
	var res []Assertion

	if cmd.Flags().Changed("ok") {
		if v, _ := cmd.Flags().GetBool("ok"); v {
			res = append(res, AssertStatusOK())
		} else {
			res = append(res, AssertStatusNOK())
		}
	}

	if cmd.Flags().Changed("redirect-to") {
		v, _ := cmd.Flags().GetString("redirect-to")
		res = append(res, AssertRedirectEqual(v))
	}
	if cmd.Flags().Changed("redirect-match") {
		v, _ := cmd.Flags().GetString("redirect-match")
		res = append(res, AssertRedirectMatch(v))
	}

	if cmd.Flags().Changed("status") {
		s, _ := cmd.Flags().GetInt("status")
		res = append(res, AssertStatus(s))
	}

	hs, _ := cmd.Flags().GetStringArray("header")
	for _, h := range hs {
		parts := strings.SplitN(h, ":", 2)
		name := strings.TrimSpace(parts[0])
		var value string
		if len(parts) > 1 {
			value = strings.TrimSpace(parts[1])
		}
		if value == "" {
			res = append(res, AssertHeaderPresent(name))
		} else {
			res = append(res, AssertHeaderEqual(name, value))
		}
	}

	hms, _ := cmd.Flags().GetStringArray("header-match")
	for _, h := range hms {
		parts := strings.SplitN(h, ":", 2)
		name := strings.TrimSpace(parts[0])
		var value string
		if len(parts) > 1 {
			value = strings.TrimSpace(parts[1])
		}
		if value == "" {
			res = append(res, AssertHeaderPresent(name))
		} else {
			res = append(res, AssertHeaderMatch(name, value))
		}
	}

	if cmd.Flags().Changed("body") {
		v, _ := cmd.Flags().GetString("body")
		res = append(res, AssertBodyEqual(v))
	}
	if cmd.Flags().Changed("body-match") {
		v, _ := cmd.Flags().GetString("body-match")
		res = append(res, AssertBodyMatch(v))
	}

	return res
}

func assertRequest(httpClient *http.Client, req *http.Request, assertions ...Assertion) error {
	if len(assertions) == 0 {
		return fmt.Errorf("no assertions defined")
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

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
		httpRes.writeTo(&b, true)
		b.WriteString("\n")
		return errors.New(b.String())
	}

	return nil
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

func getHttpClient(resolves []resolvePair) *http.Client {
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
				fmt.Printf(":: DialContext: network=%q addr=%q\n", network, addr)
				for _, r := range resolves {
					if r.Matches(addr) {
						addr = r.Addr
						break
					}
				}
				return dialer.DialContext(ctx, network, addr)
			},
		},
	}
}

type resolvePair struct {
	// Host is the resolved host (hostname + port).
	Host string
	// Addr is the destination address.
	Addr string
}

func (r resolvePair) Matches(addr string) bool {
	return r.Host == addr
}
