// Package httpassert performs an HTTP request and evaluates structured
// assertions against the response.
//
// Each call invokes the configured HTTP client once and never retries. The
// client's redirect policy may still produce a redirect chain. The package
// does not log, format results, or terminate the process; applications retain
// control over those policies.
package httpassert
