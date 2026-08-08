package main

import (
	"fmt"
	"regexp"
)

type Assertion func(res *httpResponse) error

// Pattern-based assertions are built from user input and so can fail before any
// response exists. They return an error rather than panicking; every other
// constructor in this file is infallible and returns an Assertion directly.

func AssertStatusOK() Assertion {
	return func(res *httpResponse) error {
		if s := res.StatusCode; s < 200 || s >= 400 {
			return fmt.Errorf("ok: expected OK, got %d (%q)",
				res.StatusCode, res.Status)
		}

		return nil
	}
}

func AssertStatusNOK() Assertion {
	return func(res *httpResponse) error {
		if s := res.StatusCode; s >= 200 && s < 400 {
			return fmt.Errorf("nok: expected NOK, got %d (%q)",
				res.StatusCode, res.Status)
		}

		return nil
	}
}

func AssertStatusEqual(expStatus int) Assertion {
	return func(res *httpResponse) error {
		if res.StatusCode != expStatus {
			return fmt.Errorf("status: expected %d, got %d (%q)",
				expStatus, res.StatusCode, res.Status)
		}

		return nil
	}
}

func AssertHeaderPresent(name string) Assertion {
	return func(res *httpResponse) error {
		if res.Header.Values(name) == nil {
			return fmt.Errorf("header[%s]: expected to be present, missing", name)
		}

		return nil
	}
}

func AssertHeaderMissing(name string) Assertion {
	return func(res *httpResponse) error {
		if vs := res.Header.Values(name); vs != nil {
			return fmt.Errorf("header[%s]: expected to be missing, got %q", name, vs)
		}

		return nil
	}
}

func AssertHeaderEqual(name, expValue string) Assertion {
	return func(res *httpResponse) error {
		vs := res.Header.Values(name)
		if vs == nil {
			return fmt.Errorf("header[%s]: expected %q, missing", name, expValue)
		}

		for _, v := range vs {
			if v == expValue {
				return nil
			}
		}

		return fmt.Errorf("header[%s]: expected %q, got %q", name, expValue, vs)
	}
}

func AssertHeaderMatch(name, expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return func(res *httpResponse) error {
		vs := res.Header.Values(name)
		if vs == nil {
			return fmt.Errorf("header[%s]: expected to match %q, missing",
				name, expPattern)
		}

		for _, v := range vs {
			if re.MatchString(v) {
				return nil
			}
		}

		return fmt.Errorf("header[%s]: expected to match %q, got %q", name, expPattern, vs)
	}, nil
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
	return func(res *httpResponse) error {
		body, err := bodyOf(res)
		if err != nil {
			return err
		}

		if len(body) > 0 {
			return fmt.Errorf("body: expected to be empty, got %q", string(body))
		}

		return nil
	}
}

func AssertBodyNotEmpty() Assertion {
	return func(res *httpResponse) error {
		body, err := bodyOf(res)
		if err != nil {
			return err
		}

		if len(body) == 0 {
			return fmt.Errorf("body: expected to be non-empty, got nothing")
		}

		return nil
	}
}

func AssertBodyEqual(expContent string) Assertion {
	return func(res *httpResponse) error {
		body, err := bodyOf(res)
		if err != nil {
			return err
		}

		if c := string(body); expContent != c {
			// "missing" reads better than `got ""` for the common case of an
			// empty body where content was expected, but it is a wording
			// choice inside the failure -- deciding the verdict on it is what
			// made --assert-body-eq '' impossible to satisfy (#22).
			if len(body) == 0 {
				return fmt.Errorf("body: expected %q, missing", expContent)
			}

			return fmt.Errorf("body: expected %q, got %q", expContent, c)
		}

		return nil
	}
}

func AssertBodyMatch(expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return func(res *httpResponse) error {
		body, err := bodyOf(res)
		if err != nil {
			return err
		}

		if c := string(body); !re.MatchString(c) {
			// As above: an empty body is a legitimate subject for a pattern.
			// `^$`, `.*` and `\A\z` all match it, and none of them could pass
			// while emptiness was checked before the pattern was.
			if len(body) == 0 {
				return fmt.Errorf("body: expected to match %q, missing", expPattern)
			}

			return fmt.Errorf("body: expected to match %q, got %q", expPattern, c)
		}

		return nil
	}, nil
}

func AssertRedirectEqual(expLocation string) Assertion {
	return func(res *httpResponse) error {
		if s := res.StatusCode; s < 300 || s >= 400 {
			return fmt.Errorf("redirect: wrong HTTP status: got %d (%q)",
				res.StatusCode, res.Status)
		}

		if vs := res.Header.Values("Location"); vs == nil {
			return fmt.Errorf("redirect: no Location header")
		}

		if l := res.Header.Get("Location"); l != expLocation {
			return fmt.Errorf("redirect: wrong Location: expected %q, got %q",
				expLocation, l)
		}

		return nil
	}
}

func AssertRedirectMatch(expPattern string) (Assertion, error) {
	re, err := regexp.Compile(expPattern)
	if err != nil {
		return nil, err
	}

	return func(res *httpResponse) error {
		if s := res.StatusCode; s < 300 || s >= 400 {
			return fmt.Errorf("redirect: wrong HTTP status: got %d (%q)",
				res.StatusCode, res.Status)
		}

		if vs := res.Header.Values("Location"); vs == nil {
			return fmt.Errorf("redirect: no Location header")
		}

		if l := res.Header.Get("Location"); !re.MatchString(l) {
			return fmt.Errorf("redirect: wrong Location: expected to match %q, got %q",
				expPattern, l)
		}

		return nil
	}, nil
}
