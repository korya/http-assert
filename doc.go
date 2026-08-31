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
// Client.Do consumes and closes the response body. The decoded payload remains
// available as Result.Response.BodyBytes with the original HTTP metadata.
package httpassert
