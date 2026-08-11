package main

import (
	"strings"
	"testing"
)

// Test_parseStatusSpec covers what the flag accepts and what it turns away.
//
// Rejection matters as much as acceptance here: a spec naming a code no
// response can carry is a typo in the command line, and reporting it as a
// failed assertion would tell a CI job the service is broken (#93).
func Test_parseStatusSpec(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		tests := []struct {
			Spec    string
			Matches []int
			Misses  []int
		}{
			{"200", []int{200}, []int{199, 201, 500}},
			{"2xx", []int{200, 201, 250, 299}, []int{199, 300}},
			{"2XX", []int{200, 299}, []int{300}},
			{"200,204", []int{200, 204}, []int{201, 203, 205}},
			{"200, 204", []int{200, 204}, []int{202}},
			{"401-403", []int{401, 402, 403}, []int{400, 404}},
			{"200-200", []int{200}, []int{199, 201}},
			{"301,2xx,500-503", []int{200, 299, 301, 500, 503}, []int{300, 302, 499, 504}},
			// Allowed without being advertised: a class outside 1xx-5xx is
			// non-conformant but observable, so an assertion about it is
			// answerable rather than nonsense.
			{"9xx", []int{900, 999}, []int{899}},
			{"1xx", []int{100, 199}, []int{200}},
			{"999", []int{999}, []int{998}},
			{"100", []int{100}, []int{101}},
		}

		for _, tc := range tests {
			t.Run(tc.Spec, func(t *testing.T) {
				spec, err := parseStatusSpec(tc.Spec)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if spec.text != tc.Spec {
					t.Errorf("text = %q, want %q; the failure quotes what was written", spec.text, tc.Spec)
				}
				for _, code := range tc.Matches {
					if !spec.matches(code) {
						t.Errorf("%q does not match %d, but should", tc.Spec, code)
					}
				}
				for _, code := range tc.Misses {
					if spec.matches(code) {
						t.Errorf("%q matches %d, but should not", tc.Spec, code)
					}
				}
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		tests := []struct{ Spec, Want string }{
			{"", "it is empty"},
			{"   ", "it is empty"},
			{"-1", "not a three-digit status code"},
			{"1000", "not a three-digit status code"},
			{"99", "not a three-digit status code"},
			{"20", "not a three-digit status code"},
			{"abc", "not a three-digit status code"},
			{"2x", "not a three-digit status code"},
			{"099", "no response can carry status"},
			{"000", "no response can carry status"},
			{"0xx", "not a status class"},
			{"403-401", "counts down"},
			{"200,,204", "empty entry"},
			{"200,", "empty entry"},
			{"200-", "not a three-digit status code"},
			{"200,999x", "not a three-digit status code"},
			// One bad term poisons the whole spec rather than being skipped.
			{"200,1000", "not a three-digit status code"},
		}

		for _, tc := range tests {
			t.Run(tc.Spec, func(t *testing.T) {
				_, err := parseStatusSpec(tc.Spec)
				if err == nil {
					t.Fatalf("expected %q to be rejected", tc.Spec)
				}
				if !strings.Contains(err.Error(), tc.Want) {
					t.Errorf("error = %q, want it to mention %q", err, tc.Want)
				}
			})
		}
	})
}
