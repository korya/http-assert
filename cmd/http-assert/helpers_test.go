package main

import "testing"

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
