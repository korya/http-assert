package main

import (
	"regexp"
	"testing"
)

// checkErr asserts that err says exactly what the caller expects. An empty want
// means "no error at all".
//
// This is the shape almost every assertion table in this package needs: each
// case names the error it expects, or leaves it blank to mean success. Note
// that a nil error is a failure whenever want is non-empty -- forgetting that
// is how an assertion silently stops asserting anything.
func checkErr(t *testing.T, label string, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Errorf("%s: unexpected error: %s", label, err)
		}
		return
	}

	if err == nil {
		t.Errorf("%s: expected error %q, got nil", label, want)
		return
	}

	if got := err.Error(); got != want {
		t.Errorf("%s: error = %q, want %q", label, got, want)
	}
}

// checkErrMatch is checkErr for the one case whose message contains a value
// that is not worth pinning exactly.
func checkErrMatch(t *testing.T, label string, err error, pattern string) {
	t.Helper()

	if err == nil {
		t.Errorf("%s: expected error matching %q, got nil", label, pattern)
		return
	}

	if ok, reErr := regexp.MatchString(pattern, err.Error()); reErr != nil {
		t.Fatalf("%s: bad pattern %q: %s", label, pattern, reErr)
	} else if !ok {
		t.Errorf("%s: error = %q, want match %q", label, err.Error(), pattern)
	}
}

// check runs an assertion the way doOnce does, flattening Check's two returns
// into the single error these tables compare against.
//
// The nil guard is load-bearing: returning a nil *Failure through an error
// interface yields a non-nil error holding a nil pointer, which would fail
// every "expected no error" case with an unreadable message.
func check(a Assertion, res *httpResponse) error {
	f, err := a.Check(res)
	if err != nil {
		return err
	}
	if f != nil {
		return f
	}

	return nil
}
