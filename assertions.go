package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/itchyny/gojq"
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
	// Kind names the family this assertion belongs to: "ok", "nok",
	// "status", "header", "body", "redirect" or "jq".
	Kind() string

	// Check reports (nil, nil) when the assertion holds.
	Check(res *httpResponse) (*Failure, error)
}

// Failure describes an assertion that did not hold, in parts as well as prose.
//
// Message is the human sentence, unchanged from when assertions returned a bare
// error, and is what the failure dump prints. The parts around it exist because
// prose cannot be serialized into anything a dashboard can query; they are not
// a second source of truth for the message, and no formatter derives one from
// the other. Reconstructing sentences like "expected to be non-empty, got
// nothing" from Expected and Actual alone would drift the moment a wording
// changed, and the drift would surface as a failing end-to-end test rather than
// as a compile error.
type Failure struct {
	Kind     string // filled in by Check; never set by a constructor
	Target   string // header name, jq query, or "" when the kind needs no subject
	Expected any
	Actual   any
	Message  string
}

// Error lets a Failure travel the same path as an evaluation error, so the
// caller can collect both into one list and print them in the order the
// assertions were given.
func (f *Failure) Error() string { return f.Message }

// assertionFunc adapts a closure to the Assertion interface.
//
// The functional style is what makes each constructor readable as a single
// expression, and structure was never the reason to give it up -- only the
// result needed to carry more than a string. Thirteen one-method structs would
// have said the same thing at ten times the length.
type assertionFunc struct {
	kind  string
	check func(res *httpResponse) (*Failure, error)
}

func (a assertionFunc) Kind() string { return a.kind }

// Check stamps the failure with the assertion's kind, so Kind() and
// Failure.Kind cannot disagree and no constructor has to repeat itself.
func (a assertionFunc) Check(res *httpResponse) (*Failure, error) {
	f, err := a.check(res)
	if f != nil {
		f.Kind = a.kind
	}

	return f, err
}

func newAssertion(kind string, check func(res *httpResponse) (*Failure, error)) Assertion {
	return assertionFunc{kind: kind, check: check}
}

// Pattern-based assertions are built from user input and so can fail before any
// response exists. They return an error rather than panicking; every other
// constructor in this file is infallible and returns an Assertion directly.

// headerValues renders a header's values the way the response carried them.
//
// A header can appear more than once, so the values are a list -- but %q on a
// []string prints Go's own syntax, and `got ["abc123"]` told a user reading a
// failure about a single-valued header that something bracketed had happened
// to their value (#97). One value now reads as one value, and several read as
// a list a person would write.
func headerValues(vs []string) string {
	quoted := make([]string, len(vs))
	for i, v := range vs {
		quoted[i] = strconv.Quote(v)
	}

	return strings.Join(quoted, ", ")
}

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

func AssertStatusOK() Assertion {
	return newAssertion("ok", func(res *httpResponse) (*Failure, error) {
		if s := res.StatusCode; s < 200 || s >= 400 {
			return &Failure{
				Expected: "2xx-3xx",
				Actual:   res.StatusCode,
				Message: fmt.Sprintf("ok: expected OK, got %d (%q)",
					res.StatusCode, res.Status),
			}, nil
		}

		return nil, nil
	})
}

func AssertStatusNOK() Assertion {
	return newAssertion("nok", func(res *httpResponse) (*Failure, error) {
		if s := res.StatusCode; s >= 200 && s < 400 {
			return &Failure{
				Expected: "not 2xx-3xx",
				Actual:   res.StatusCode,
				Message: fmt.Sprintf("nok: expected NOK, got %d (%q)",
					res.StatusCode, res.Status),
			}, nil
		}

		return nil, nil
	})
}

// AssertStatus holds when the response carries any status the spec names.
func AssertStatus(spec statusSpec) Assertion {
	return newAssertion("status", func(res *httpResponse) (*Failure, error) {
		if !spec.matches(res.StatusCode) {
			return &Failure{
				Expected: spec.text,
				Actual:   res.StatusCode,
				Message: fmt.Sprintf("status: expected %s, got %d (%q)",
					spec.text, res.StatusCode, res.Status),
			}, nil
		}

		return nil, nil
	})
}

func AssertHeaderPresent(name string) Assertion {
	return newAssertion("header", func(res *httpResponse) (*Failure, error) {
		if res.Header.Values(name) == nil {
			return &Failure{
				Target:   name,
				Expected: "present",
				Message: fmt.Sprintf("header[%s]: expected to be present, missing",
					name),
			}, nil
		}

		return nil, nil
	})
}

func AssertHeaderMissing(name string) Assertion {
	return newAssertion("header", func(res *httpResponse) (*Failure, error) {
		if vs := res.Header.Values(name); vs != nil {
			return &Failure{
				Target:   name,
				Expected: "missing",
				Actual:   vs,
				Message: fmt.Sprintf("header[%s]: expected to be missing, got %s",
					name, headerValues(vs)),
			}, nil
		}

		return nil, nil
	})
}

func AssertHeaderEqual(name, expValue string) Assertion {
	return newAssertion("header", func(res *httpResponse) (*Failure, error) {
		vs := res.Header.Values(name)
		if vs == nil {
			return &Failure{
				Target:   name,
				Expected: expValue,
				Message: fmt.Sprintf("header[%s]: expected %q, missing",
					name, expValue),
			}, nil
		}

		for _, v := range vs {
			if v == expValue {
				return nil, nil
			}
		}

		return &Failure{
			Target:   name,
			Expected: expValue,
			Actual:   vs,
			Message: fmt.Sprintf("header[%s]: expected %q, got %s",
				name, expValue, headerValues(vs)),
		}, nil
	})
}

func AssertHeaderMatch(name, expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return newAssertion("header", func(res *httpResponse) (*Failure, error) {
		vs := res.Header.Values(name)
		if vs == nil {
			return &Failure{
				Target:   name,
				Expected: expPattern,
				Message: fmt.Sprintf("header[%s]: expected to match %q, missing",
					name, expPattern),
			}, nil
		}

		for _, v := range vs {
			if re.MatchString(v) {
				return nil, nil
			}
		}

		return &Failure{
			Target:   name,
			Expected: expPattern,
			Actual:   vs,
			Message: fmt.Sprintf("header[%s]: expected to match %q, got %s",
				name, expPattern, headerValues(vs)),
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
func bodyOf(res *httpResponse) ([]byte, error) {
	if res.DecodeErr != nil {
		return nil, fmt.Errorf("body: response is %s-encoded and was not decoded: %s",
			res.Encoding, res.DecodeErr)
	}

	return res.BodyBytes, nil
}

func AssertBodyEmpty() Assertion {
	return newAssertion("body", func(res *httpResponse) (*Failure, error) {
		body, err := bodyOf(res)
		if err != nil {
			return nil, err
		}

		if len(body) > 0 {
			return &Failure{
				Expected: "empty",
				Actual:   string(body),
				Message: fmt.Sprintf("body: expected to be empty, got %q",
					string(body)),
			}, nil
		}

		return nil, nil
	})
}

// jqTimeout bounds the evaluation of one --assert-jq query.
//
// jq is a real language, so a query can simply never finish: `def f: f; f`
// compiles cleanly and runs forever. Nothing else a caller can type makes this
// program hang -- the request is bounded by --max-time, the retry loop by
// --retry, and an --assert-body pattern cannot blow up because Go's regexp
// engine is linear-time. This keeps that property rather than trading it away.
//
// It is not a budget for real work, and deliberately is not --max-time: that
// bounds a request, and reusing it here would make a run take twice the number
// the caller set. Measured queries finish in tens of microseconds, so ten
// seconds is six orders of magnitude of headroom that no genuine assertion can
// reach.
const jqTimeout = 10 * time.Second

// AssertJQ asserts that a jq expression holds against the response body.
//
// The expression yields the verdict itself rather than a path and an expected
// value, which is what keeps this to one flag: jq already has types,
// comparison and regexp, so there is no separator to invent, no ~= variant,
// and no question of whether 5 means the number or the string.
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

	return newAssertion("jq", func(res *httpResponse) (*Failure, error) {
		return runJQ(code, query, res, jqTimeout)
	}), nil
}

// runJQ evaluates one compiled query. The deadline is a parameter so the test
// for it need not wait out the real one; every caller passes jqTimeout.
func runJQ(code *gojq.Code, query string, res *httpResponse, timeout time.Duration) (*Failure, error) {
	doc, err := res.decodeJSON()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
			return nil, fmt.Errorf("jq[%s]: %s", query, e)
		}

		if b, isBool := v.(bool); !isBool || !b {
			return &Failure{
				Target:   query,
				Expected: true,
				Actual:   v,
				Message: fmt.Sprintf("jq[%s]: expected true, got %s",
					query, jqValue(v)),
			}, nil
		}
	}

	// A query that matched nothing checked nothing. Reporting that as a
	// pass is the one outcome this program exists to refuse, and it is
	// reachable by accident: `.users[] | select(.id == 99) | .active`
	// yields no output at all when no user has that id.
	if outputs == 0 {
		return &Failure{
			Target:   query,
			Expected: true,
			Message:  fmt.Sprintf("jq[%s]: expected true, got no output", query),
		}, nil
	}

	return nil, nil
}

// jqValue renders a query's output for the failure message. jq's own notation
// is JSON, so this is what `jq` would have printed for the same expression.
func jqValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}

	return string(b)
}

func AssertBodyNotEmpty() Assertion {
	return newAssertion("body", func(res *httpResponse) (*Failure, error) {
		body, err := bodyOf(res)
		if err != nil {
			return nil, err
		}

		if len(body) == 0 {
			return &Failure{
				Expected: "non-empty",
				Message:  "body: expected to be non-empty, got nothing",
			}, nil
		}

		return nil, nil
	})
}

func AssertBodyEqual(expContent string) Assertion {
	return newAssertion("body", func(res *httpResponse) (*Failure, error) {
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
					Expected: expContent,
					Message: fmt.Sprintf("body: expected %q, missing",
						expContent),
				}, nil
			}

			return &Failure{
				Expected: expContent,
				Actual:   c,
				Message:  fmt.Sprintf("body: expected %q, got %q", expContent, c),
			}, nil
		}

		return nil, nil
	})
}

func AssertBodyMatch(expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return newAssertion("body", func(res *httpResponse) (*Failure, error) {
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
					Expected: expPattern,
					Message: fmt.Sprintf("body: expected to match %q, missing",
						expPattern),
				}, nil
			}

			return &Failure{
				Expected: expPattern,
				Actual:   c,
				Message: fmt.Sprintf("body: expected to match %q, got %q",
					expPattern, c),
			}, nil
		}

		return nil, nil
	}), nil
}

// redirectPrecondition reports the two ways a redirect assertion fails before
// its Location is compared at all. Both redirect assertions share them, and
// sharing the code is what keeps their wording identical.
func redirectPrecondition(res *httpResponse, expected any) *Failure {
	if s := res.StatusCode; s < 300 || s >= 400 {
		return &Failure{
			Expected: "3xx",
			Actual:   res.StatusCode,
			Message: fmt.Sprintf("redirect: wrong HTTP status: got %d (%q)",
				res.StatusCode, res.Status),
		}
	}

	if vs := res.Header.Values("Location"); vs == nil {
		return &Failure{
			Target:   "Location",
			Expected: expected,
			Message:  "redirect: no Location header",
		}
	}

	return nil
}

func AssertRedirectEqual(expLocation string) Assertion {
	return newAssertion("redirect", func(res *httpResponse) (*Failure, error) {
		if f := redirectPrecondition(res, expLocation); f != nil {
			return f, nil
		}

		if l := res.Header.Get("Location"); l != expLocation {
			return &Failure{
				Target:   "Location",
				Expected: expLocation,
				Actual:   l,
				Message: fmt.Sprintf("redirect: wrong Location: expected %q, got %q",
					expLocation, l),
			}, nil
		}

		return nil, nil
	})
}

func AssertRedirectMatch(expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return newAssertion("redirect", func(res *httpResponse) (*Failure, error) {
		if f := redirectPrecondition(res, expPattern); f != nil {
			return f, nil
		}

		if l := res.Header.Get("Location"); !re.MatchString(l) {
			return &Failure{
				Target:   "Location",
				Expected: expPattern,
				Actual:   l,
				Message: fmt.Sprintf("redirect: wrong Location: expected to match %q, got %q",
					expPattern, l),
			}, nil
		}

		return nil, nil
	}), nil
}
