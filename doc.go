// Package httpassert performs an HTTP request and evaluates structured
// assertions against the response.
//
// Each call invokes the configured HTTP client once and never retries. The
// client's redirect policy may still produce a redirect chain. The package
// does not log, format results, or terminate the process; applications retain
// control over those policies.
//
// A nil top-level error means Result.Outcomes contains one structured outcome
// per assertion. Failures describe responses that did not satisfy an assertion;
// evaluation errors describe assertions that could not reach a verdict.
//
// Client.Do consumes and closes the response body. After a nil top-level error,
// the decoded payload remains available as Result.Response.BodyBytes with the
// original HTTP metadata. A body-read error instead leaves the partial encoded
// bytes in BodyBytes and returns a non-nil top-level error.
package httpassert
