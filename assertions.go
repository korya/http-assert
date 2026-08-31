package httpassert

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// AssertionKind identifies an assertion family. Custom assertions may define
// their own values; the constants below name the families built into this
// package.
type AssertionKind string

const (
	KindStatusOK  AssertionKind = "ok"
	KindStatusNOK AssertionKind = "nok"
	KindStatus    AssertionKind = "status"
	KindHeader    AssertionKind = "header"
	KindBody      AssertionKind = "body"
	KindRedirect  AssertionKind = "redirect"
	KindJQ        AssertionKind = "jq"
)

// Assertion checks one property of a response.
//
// Check separates the two things an assertion can report, which a single error
// return conflated: a *Failure means the response was read and did not hold up,
// an error means the assertion could not be evaluated at all -- an undecodable
// body, a jq runtime fault, a body that is not JSON. Both still count as a
// failed run; the distinction exists so a machine-readable consumer can tell
// "the service is wrong" from "we could not tell" (#45).
type Assertion interface {
	// Kind names the family this assertion belongs to.
	Kind() AssertionKind

	// Check reports exactly one of three states: (nil, nil) when the assertion
	// holds; (failure, nil) when the response does not satisfy it; or
	// (nil, error) when no verdict could be reached. Implementations must not
	// return both a failure and an error. Client.Do treats an error as
	// authoritative if a custom implementation violates that contract.
	Check(res *Response) (*Failure, error)
}

// FailureCode identifies why an assertion did not hold without prescribing how
// a caller presents that fact.
type FailureCode string

const (
	FailureStatusOK               FailureCode = "status_ok"
	FailureStatusNOK              FailureCode = "status_nok"
	FailureStatus                 FailureCode = "status"
	FailureHeaderPresent          FailureCode = "header_present"
	FailureHeaderMissing          FailureCode = "header_missing"
	FailureHeaderEqual            FailureCode = "header_equal"
	FailureHeaderMatch            FailureCode = "header_match"
	FailureBodyEmpty              FailureCode = "body_empty"
	FailureBodyNotEmpty           FailureCode = "body_not_empty"
	FailureBodyEqual              FailureCode = "body_equal"
	FailureBodyMatch              FailureCode = "body_match"
	FailureJQValue                FailureCode = "jq_value"
	FailureJQNoOutput             FailureCode = "jq_no_output"
	FailureRedirectStatus         FailureCode = "redirect_status"
	FailureRedirectLocationAbsent FailureCode = "redirect_location_absent"
	FailureRedirectEqual          FailureCode = "redirect_equal"
	FailureRedirectMatch          FailureCode = "redirect_match"
)

// Failure describes an assertion that was evaluated and did not hold. It is
// deliberately data only: applications decide how (or whether) to format it.
type Failure struct {
	Kind     AssertionKind // assertion family; Client and built-in assertions populate it
	Code     FailureCode
	Target   string // header name, jq query, or "" when the kind needs no subject
	Expected any
	Actual   any
}

// assertionFunc adapts a closure to the Assertion interface.
//
// The functional style is what makes each constructor readable as a single
// expression, and structure was never the reason to give it up -- only the
// result needed to carry more than a string. Thirteen one-method structs would
// have said the same thing at ten times the length.
type assertionFunc struct {
	kind  AssertionKind
	check func(res *Response) (*Failure, error)
}

func (a assertionFunc) Kind() AssertionKind { return a.kind }

// Check normalizes the outcome so its structured kind cannot disagree with
// Kind() and no constructor has to repeat itself.
func (a assertionFunc) Check(res *Response) (*Failure, error) {
	f, err := a.check(res)
	return normalizeOutcome(a.kind, f, err)
}

// normalizeOutcome stamps structured results with the assertion family and
// defensively preserves the interface's mutually exclusive result states.
// An evaluation error wins over a failure because it means no valid verdict
// exists to describe as Expected versus Actual.
func normalizeOutcome(kind AssertionKind, failure *Failure, err error) (*Failure, error) {
	if err != nil {
		var evaluation *EvaluationError
		if errors.As(err, &evaluation) && evaluation != nil {
			evaluation.Kind = kind
		}
		return nil, err
	}
	if failure != nil {
		failure.Kind = kind
	}
	return failure, nil
}

func newAssertion(kind AssertionKind, check func(res *Response) (*Failure, error)) Assertion {
	return assertionFunc{kind: kind, check: check}
}

// Pattern-based assertions are built from user input and so can fail before any
// response exists. They return an error rather than panicking; every other
// constructor in this file is infallible and returns an Assertion directly.

// Status codes a response can actually carry. net/http refuses to write
// anything outside this range -- 99 and 1000 panic in WriteHeader -- so a spec
// naming one of them is a typo in the invocation rather than a fact about the
// service, and it is rejected before a request is made.
//
// The upper bound is 999 rather than 599 because 6xx-9xx are non-conformant
// but observable: a server can send them and net/http reports them faithfully,
// so an assertion about one is answerable.
const (
	statusMin = 100
	statusMax = 999
)

// statusRange is an inclusive span of status codes. A single code is a span of
// one, so every form --assert-status accepts reduces to the same shape.
type statusRange struct{ lo, hi int }

// statusSpec is the set of status codes an assertion will accept.
//
// text is kept as the caller wrote it: a failure saying `expected 2xx` is the
// question they asked, where `expected 200-299` would be an answer they would
// have to translate back.
type statusSpec struct {
	text   string
	ranges []statusRange
}

func (s statusSpec) matches(code int) bool {
	for _, r := range s.ranges {
		if code >= r.lo && code <= r.hi {
			return true
		}
	}

	return false
}

// parseStatusSpec reads the forms --assert-status accepts: an exact code, a
// class like 2xx, an inclusive range like 401-403, or a comma-separated list
// mixing any of them.
//
// Parsed by hand rather than by regexp, which the linter forbids compiling
// from user input anyway, and which would be longer than the three cases it
// replaced.
func parseStatusSpec(text string) (statusSpec, error) {
	spec := statusSpec{text: text}
	if strings.TrimSpace(text) == "" {
		return spec, fmt.Errorf("it is empty; give a status code, a class like 2xx, or a range like 401-403")
	}

	for _, term := range strings.Split(text, ",") {
		r, err := parseStatusTerm(strings.TrimSpace(term))
		if err != nil {
			return spec, err
		}
		spec.ranges = append(spec.ranges, r)
	}

	return spec, nil
}

func parseStatusTerm(term string) (statusRange, error) {
	if term == "" {
		return statusRange{}, fmt.Errorf("it has an empty entry; remove the stray comma")
	}

	// A class: the leading digit fixes the hundred, xx covers the rest.
	if len(term) == 3 && isX(term[1]) && isX(term[2]) {
		d := term[0]
		if d < '1' || d > '9' {
			return statusRange{}, fmt.Errorf("%q is not a status class; the leading digit is 1 to 9", term)
		}
		lo := int(d-'0') * 100

		return statusRange{lo, lo + 99}, nil
	}

	// A range. Both ends must be present, so a negative number falls through
	// to be reported as the code it is not, rather than as a range with an
	// empty low end -- which named "" in the error and not what was typed.
	if loText, hiText, isRange := strings.Cut(term, "-"); isRange && loText != "" && hiText != "" {
		lo, err := parseStatusCode(loText)
		if err != nil {
			return statusRange{}, err
		}
		hi, err := parseStatusCode(hiText)
		if err != nil {
			return statusRange{}, err
		}
		if lo > hi {
			return statusRange{}, fmt.Errorf("range %q counts down; write it low to high", term)
		}

		return statusRange{lo, hi}, nil
	}

	code, err := parseStatusCode(term)

	return statusRange{code, code}, err
}

func isX(b byte) bool { return b == 'x' || b == 'X' }

func parseStatusCode(text string) (int, error) {
	if len(text) != 3 {
		return 0, fmt.Errorf("%q is not a three-digit status code", text)
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, fmt.Errorf("%q is not a three-digit status code", text)
		}
	}

	code, _ := strconv.Atoi(text) // three digits, so it cannot overflow
	if code < statusMin || code > statusMax {
		return 0, fmt.Errorf("no response can carry status %s; codes run %d to %d",
			text, statusMin, statusMax)
	}

	return code, nil
}

// AssertStatusOK accepts any success or redirect status (2xx or 3xx).
func AssertStatusOK() Assertion {
	return newAssertion(KindStatusOK, func(res *Response) (*Failure, error) {
		if s := res.StatusCode; s < 200 || s >= 400 {
			return &Failure{
				Code:     FailureStatusOK,
				Expected: "2xx-3xx",
				Actual:   res.StatusCode,
			}, nil
		}

		return nil, nil
	})
}

// AssertStatusNOK accepts any status outside the 2xx and 3xx ranges.
func AssertStatusNOK() Assertion {
	return newAssertion(KindStatusNOK, func(res *Response) (*Failure, error) {
		if s := res.StatusCode; s >= 200 && s < 400 {
			return &Failure{
				Code:     FailureStatusNOK,
				Expected: "not 2xx-3xx",
				Actual:   res.StatusCode,
			}, nil
		}

		return nil, nil
	})
}

// AssertStatus builds an assertion accepting any status named by text. Text may
// be a code, a class such as "2xx", an inclusive range, or a comma-separated
// list mixing those forms.
func AssertStatus(text string) (Assertion, error) {
	spec, err := parseStatusSpec(text)
	if err != nil {
		return nil, err
	}

	return assertStatus(spec), nil
}

func assertStatus(spec statusSpec) Assertion {
	return newAssertion(KindStatus, func(res *Response) (*Failure, error) {
		if !spec.matches(res.StatusCode) {
			return &Failure{
				Code:     FailureStatus,
				Expected: spec.text,
				Actual:   res.StatusCode,
			}, nil
		}

		return nil, nil
	})
}

// AssertHeaderPresent requires at least one value for name.
func AssertHeaderPresent(name string) Assertion {
	return newAssertion(KindHeader, func(res *Response) (*Failure, error) {
		if res.Header.Values(name) == nil {
			return &Failure{
				Code:     FailureHeaderPresent,
				Target:   name,
				Expected: "present",
			}, nil
		}

		return nil, nil
	})
}

// AssertHeaderMissing requires name to be absent.
func AssertHeaderMissing(name string) Assertion {
	return newAssertion(KindHeader, func(res *Response) (*Failure, error) {
		if vs := res.Header.Values(name); vs != nil {
			return &Failure{
				Code:     FailureHeaderMissing,
				Target:   name,
				Expected: "missing",
				Actual:   vs,
			}, nil
		}

		return nil, nil
	})
}

// AssertHeaderEqual accepts the response when any value of name equals
// expValue.
func AssertHeaderEqual(name, expValue string) Assertion {
	return newAssertion(KindHeader, func(res *Response) (*Failure, error) {
		vs := res.Header.Values(name)
		if vs == nil {
			return &Failure{
				Code:     FailureHeaderEqual,
				Target:   name,
				Expected: expValue,
			}, nil
		}

		for _, v := range vs {
			if v == expValue {
				return nil, nil
			}
		}

		return &Failure{
			Code:     FailureHeaderEqual,
			Target:   name,
			Expected: expValue,
			Actual:   vs,
		}, nil
	})
}

// AssertHeaderMatch accepts the response when any value of name matches the Go
// regular expression expPattern.
func AssertHeaderMatch(name, expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return newAssertion(KindHeader, func(res *Response) (*Failure, error) {
		vs := res.Header.Values(name)
		if vs == nil {
			return &Failure{
				Code:     FailureHeaderMatch,
				Target:   name,
				Expected: expPattern,
			}, nil
		}

		for _, v := range vs {
			if re.MatchString(v) {
				return nil, nil
			}
		}

		return &Failure{
			Code:     FailureHeaderMatch,
			Target:   name,
			Expected: expPattern,
			Actual:   vs,
		}, nil
	}), nil
}

// bodyOf returns the payload, or an error saying why there is none to assert
// against.
//
// Every body assertion goes through it. Matching against bytes that are still
// compressed produced a false failure with an unexplained hex dump -- and, with
// a loose enough pattern, a false pass, which is the one outcome this program
// exists to refuse (#27).
func bodyOf(res *Response) ([]byte, error) {
	if res.DecodeErr != nil {
		return nil, &EvaluationError{
			Code:     EvaluationBodyDecode,
			Kind:     KindBody,
			Encoding: res.Encoding,
			Cause:    res.DecodeErr,
		}
	}

	return res.BodyBytes, nil
}

// AssertBodyEmpty requires the decoded response body to contain zero bytes.
func AssertBodyEmpty() Assertion {
	return newAssertion(KindBody, func(res *Response) (*Failure, error) {
		body, err := bodyOf(res)
		if err != nil {
			return nil, err
		}

		if len(body) > 0 {
			return &Failure{
				Code:     FailureBodyEmpty,
				Expected: "empty",
				Actual:   string(body),
			}, nil
		}

		return nil, nil
	})
}

// jqTimeout bounds the evaluation of one --assert-jq query.
//
// jq is a real language, so a query can simply never finish: `def f: f; f`
// compiles cleanly and runs forever. The response request's context is the
// primary cancellation signal, so its earlier deadline wins. This timeout is
// the backstop for a context without a deadline and for a Response constructed
// directly without an http.Request.
//
// Measured queries finish in tens of microseconds, so ten seconds is six orders
// of magnitude of headroom that no genuine assertion can reach.
const jqTimeout = 10 * time.Second

// AssertJQ asserts that a jq expression holds against the response body.
//
// The expression yields the verdict itself rather than a path and an expected
// value, which is what keeps this to one flag: jq already has types,
// comparison and regexp, so there is no separator to invent, no ~= variant,
// and no question of whether 5 means the number or the string.
// Evaluation stops when the response request's context is cancelled and is
// always bounded by the package's jq timeout.
func AssertJQ(query string) (Assertion, error) {
	q, err := gojq.Parse(query)
	if err != nil {
		return nil, err
	}

	// Compiled as well as parsed, because the two reject different things:
	// `no_such_func(.)` and `. as $x | $y` are syntactically valid and fail
	// only here. Catching them now is what makes a typo exit 71 against the
	// flag rather than 93 in the middle of a run.
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, err
	}

	return newAssertion(KindJQ, func(res *Response) (*Failure, error) {
		return runJQ(code, query, res, jqTimeout)
	}), nil
}

// runJQ evaluates one compiled query. The deadline is a parameter so the test
// for it need not wait out the real one; every caller passes jqTimeout.
func runJQ(code *gojq.Code, query string, res *Response, timeout time.Duration) (*Failure, error) {
	doc, err := res.decodeJSON()
	if err != nil {
		return nil, err
	}

	parent := context.Background()
	if res.Response != nil && res.Request != nil {
		parent = res.Request.Context()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	outputs := 0
	it := code.RunWithContext(ctx, doc)
	for {
		v, ok := it.Next()
		if !ok {
			break
		}
		outputs++

		// gojq reports a runtime failure as an error *value* in the output
		// stream rather than as a Go error. Untyped it would read as "not
		// true", turning a broken query into a failed assertion and
		// sending the reader to inspect a service that answered correctly.
		//
		// It is an error rather than a Failure for the same reason: the
		// query never reached a verdict, so there is nothing for Expected
		// and Actual to describe.
		if e, isErr := v.(error); isErr {
			return nil, &EvaluationError{
				Code:   EvaluationJQ,
				Kind:   KindJQ,
				Target: query,
				Cause:  e,
			}
		}

		if b, isBool := v.(bool); !isBool || !b {
			return &Failure{
				Code:     FailureJQValue,
				Target:   query,
				Expected: true,
				Actual:   v,
			}, nil
		}
	}

	// A query that matched nothing checked nothing. Reporting that as a
	// pass is the one outcome this program exists to refuse, and it is
	// reachable by accident: `.users[] | select(.id == 99) | .active`
	// yields no output at all when no user has that id.
	if outputs == 0 {
		return &Failure{
			Code:     FailureJQNoOutput,
			Target:   query,
			Expected: true,
		}, nil
	}

	return nil, nil
}

// AssertBodyNotEmpty requires the decoded response body to contain at least
// one byte.
func AssertBodyNotEmpty() Assertion {
	return newAssertion(KindBody, func(res *Response) (*Failure, error) {
		body, err := bodyOf(res)
		if err != nil {
			return nil, err
		}

		if len(body) == 0 {
			return &Failure{
				Code:     FailureBodyNotEmpty,
				Expected: "non-empty",
			}, nil
		}

		return nil, nil
	})
}

// AssertBodyEqual requires the decoded response body to equal expContent.
func AssertBodyEqual(expContent string) Assertion {
	return newAssertion(KindBody, func(res *Response) (*Failure, error) {
		body, err := bodyOf(res)
		if err != nil {
			return nil, err
		}

		if c := string(body); expContent != c {
			// "missing" reads better than `got ""` for the common case of an
			// empty body where content was expected, but it is a wording
			// choice inside the failure -- deciding the verdict on it is what
			// made --assert-body-eq '' impossible to satisfy (#22).
			if len(body) == 0 {
				return &Failure{
					Code:     FailureBodyEqual,
					Expected: expContent,
				}, nil
			}

			return &Failure{
				Code:     FailureBodyEqual,
				Expected: expContent,
				Actual:   c,
			}, nil
		}

		return nil, nil
	})
}

// AssertBodyMatch requires the decoded response body to match the Go regular
// expression expPattern.
func AssertBodyMatch(expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return newAssertion(KindBody, func(res *Response) (*Failure, error) {
		body, err := bodyOf(res)
		if err != nil {
			return nil, err
		}

		if c := string(body); !re.MatchString(c) {
			// As above: an empty body is a legitimate subject for a pattern.
			// `^$`, `.*` and `\A\z` all match it, and none of them could pass
			// while emptiness was checked before the pattern was.
			if len(body) == 0 {
				return &Failure{
					Code:     FailureBodyMatch,
					Expected: expPattern,
				}, nil
			}

			return &Failure{
				Code:     FailureBodyMatch,
				Expected: expPattern,
				Actual:   c,
			}, nil
		}

		return nil, nil
	}), nil
}

// redirectPrecondition reports the two ways a redirect assertion fails before
// its Location is compared at all. Both redirect assertions share them, and
// sharing the code is what keeps their wording identical.
func redirectPrecondition(res *Response, expected any) *Failure {
	if s := res.StatusCode; s < 300 || s >= 400 {
		return &Failure{
			Code:     FailureRedirectStatus,
			Expected: "3xx",
			Actual:   res.StatusCode,
		}
	}

	if vs := res.Header.Values("Location"); vs == nil {
		return &Failure{
			Code:     FailureRedirectLocationAbsent,
			Target:   "Location",
			Expected: expected,
		}
	}

	return nil
}

// AssertRedirectEqual requires a 3xx response whose Location equals
// expLocation. Configure the HTTP client not to follow redirects when using
// this assertion.
func AssertRedirectEqual(expLocation string) Assertion {
	return newAssertion(KindRedirect, func(res *Response) (*Failure, error) {
		if f := redirectPrecondition(res, expLocation); f != nil {
			return f, nil
		}

		if l := res.Header.Get("Location"); l != expLocation {
			return &Failure{
				Code:     FailureRedirectEqual,
				Target:   "Location",
				Expected: expLocation,
				Actual:   l,
			}, nil
		}

		return nil, nil
	})
}

// AssertRedirectMatch requires a 3xx response whose Location matches the Go
// regular expression expPattern. Configure the HTTP client not to follow
// redirects when using this assertion.
func AssertRedirectMatch(expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return newAssertion(KindRedirect, func(res *Response) (*Failure, error) {
		if f := redirectPrecondition(res, expPattern); f != nil {
			return f, nil
		}

		if l := res.Header.Get("Location"); !re.MatchString(l) {
			return &Failure{
				Code:     FailureRedirectMatch,
				Target:   "Location",
				Expected: expPattern,
				Actual:   l,
			}, nil
		}

		return nil, nil
	}), nil
}
