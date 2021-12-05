package main

import (
	"fmt"
	"regexp"
)

type Assertion func(res *httpResponse) error

func AssertStatusOK() Assertion {
	return func(res *httpResponse) error {
		if s := res.StatusCode; s < 200 || s >= 300 {
			return fmt.Errorf("ok: expected OK, got=%d (%q)",
				res.StatusCode, res.Status)
		}

		return nil
	}
}

func AssertStatusNOK() Assertion {
	return func(res *httpResponse) error {
		if s := res.StatusCode; s > 200 && s < 300 {
			return fmt.Errorf("nok: expected NOK, got=%d (%q)",
				res.StatusCode, res.Status)
		}

		return nil
	}
}

func AssertStatus(expStatus int) Assertion {
	return func(res *httpResponse) error {
		if res.StatusCode != expStatus {
			return fmt.Errorf("status: expected %d, got=%d (%q)",
				expStatus, res.StatusCode, res.Status)
		}

		return nil
	}
}

func AssertHeaderPresent(name string) Assertion {
	return func(res *httpResponse) error {
		if v := res.Header.Get(name); v == "" {
			return fmt.Errorf("header[%s]: expected to be present, missing",
				name)
		}

		return nil
	}
}

func AssertHeaderEqual(name, expValue string) Assertion {
	return func(res *httpResponse) error {
		if v := res.Header.Get(name); v != expValue {
			return fmt.Errorf("header[%s]: expected %q, got=%q",
				name, expValue, v)
		}

		return nil
	}
}

func AssertHeaderMatch(name, expPattern string) Assertion {
	re := regexp.MustCompile(expPattern)

	return func(res *httpResponse) error {
		if v := res.Header.Get(name); !re.MatchString(v) {
			return fmt.Errorf("header[%s]: expected to match %q, got=%q",
				name, expPattern, v)
		}

		return nil
	}
}

func AssertRedirectEqual(expLocation string) Assertion {
	return func(res *httpResponse) error {
		if s := res.StatusCode; s < 300 || s >= 400 {
			return fmt.Errorf("redirect: wrong HTTP status: got=%d (%q)",
				res.StatusCode, res.Status)
		}

		l := res.Header.Get("Location")
		if l == "" {
			return fmt.Errorf("redirect: no Location header")
		}

		if l != expLocation {
			return fmt.Errorf("redirect: wrong Location: expected %q, got %q",
				expLocation, l)
		}

		return nil
	}
}

func AssertRedirectMatch(expPattern string) Assertion {
	re := regexp.MustCompile(expPattern)

	return func(res *httpResponse) error {
		if s := res.StatusCode; s < 300 || s >= 400 {
			return fmt.Errorf("redirect: wrong HTTP status: got=%d (%q)",
				res.StatusCode, res.Status)
		}

		l := res.Header.Get("Location")
		if l == "" {
			return fmt.Errorf("redirect: no Location header")
		}

		if !re.MatchString(l) {
			return fmt.Errorf("redirect: wrong Location: expected to match %q, got %q",
				expPattern, l)
		}

		return nil
	}
}

func AssertBodyEqual(expContent string) Assertion {
	return func(res *httpResponse) error {
		if c := string(res.BodyBytes); expContent != c {
			return fmt.Errorf("body: expected %q, got %q",
				expContent, c)
		}

		return nil
	}
}

func AssertBodyMatch(expPattern string) Assertion {
	re := regexp.MustCompile(expPattern)

	return func(res *httpResponse) error {
		if c := string(res.BodyBytes); !re.MatchString(c) {
			return fmt.Errorf("body: expected to match %q, got %q",
				expPattern, c)
		}

		return nil
	}
}
