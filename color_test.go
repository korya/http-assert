package main

import (
	"strings"
	"testing"
)

// Test_shouldColor is the whole decision, which is why it takes its three
// inputs as arguments rather than reading a terminal and an environment.
func Test_shouldColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Name    string
		Mode    string
		NoColor string
		IsTTY   bool
		Want    bool
		WantErr bool
	}{
		{Name: "auto on a terminal", Mode: "auto", IsTTY: true, Want: true},
		{Name: "auto in a pipe", Mode: "auto", IsTTY: false, Want: false},
		// The case the default exists for: a CI log is not a terminal, so it
		// stays plain without anyone having to ask.
		{Name: "auto with NO_COLOR on a terminal", Mode: "auto", NoColor: "1", IsTTY: true, Want: false},
		{Name: "auto with NO_COLOR set to anything", Mode: "auto", NoColor: "0", IsTTY: true, Want: false},
		// NO_COLOR's own wording: empty counts as unset.
		{Name: "auto with an empty NO_COLOR", Mode: "auto", NoColor: "", IsTTY: true, Want: true},

		{Name: "always in a pipe", Mode: "always", IsTTY: false, Want: true},
		// A variable says what to do absent an instruction; the flag is one.
		{Name: "always beats NO_COLOR", Mode: "always", NoColor: "1", IsTTY: false, Want: true},

		{Name: "never on a terminal", Mode: "never", IsTTY: true, Want: false},
		{Name: "never with NO_COLOR unset", Mode: "never", IsTTY: true, Want: false},

		{Name: "an unknown mode is rejected", Mode: "purple", WantErr: true},
		{Name: "an empty mode is rejected", Mode: "", WantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := shouldColor(tc.Mode, tc.NoColor, tc.IsTTY)
			if tc.WantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got nil", tc.Mode)
				}
				if !strings.Contains(err.Error(), "auto, always, never") {
					t.Errorf("error = %q, want it to list the values", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if got != tc.Want {
				t.Errorf("shouldColor(%q, %q, %v) = %v, want %v",
					tc.Mode, tc.NoColor, tc.IsTTY, got, tc.Want)
			}
		})
	}
}

// Test_paletteLine pins which lines are coloured and how. The sigil decides,
// so a new sigil that nobody adds here is left plain rather than mis-coloured.
func Test_paletteLine(t *testing.T) {
	t.Parallel()

	on := palette{on: true}

	tests := []struct {
		Name string
		In   string
		Want string
	}{
		{"the passing verdict is green", "[+] PASSED 1ms\n", ansiGreen + "[+] PASSED 1ms" + ansiReset + "\n"},
		{"the failing verdict is red", "[-] FAILED 1ms\n", ansiRed + "[-] FAILED 1ms" + ansiReset + "\n"},
		{"the request line is dimmed", "[.] GET /\n", ansiDim + "[.] GET /" + ansiReset + "\n"},
		{"the response line is dimmed", "[:] 200 OK\n", ansiDim + "[:] 200 OK" + ansiReset + "\n"},
		{"the redirect line is dimmed", "[>] /next\n", ansiDim + "[>] /next" + ansiReset + "\n"},
		{"the wait line is dimmed", "[~] waiting 1s\n", ansiDim + "[~] waiting 1s" + ansiReset + "\n"},
		{"an unsigilled line is left alone", "plain text\n", "plain text\n"},
		// The reset belongs before the blank line, or a terminal paints it.
		{"trailing newlines stay outside the sequence", "[+] PASSED\n\n", ansiGreen + "[+] PASSED" + ansiReset + "\n\n"},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			if got := on.line(tc.In); got != tc.Want {
				t.Errorf("line(%q) = %q, want %q", tc.In, got, tc.Want)
			}
		})
	}

	// The zero value is the one every path before flag parsing uses.
	t.Run("the zero palette writes no colour", func(t *testing.T) {
		var off palette
		for _, in := range []string{"[+] PASSED\n", "[-] FAILED\n", "[.] GET /\n"} {
			if got := off.line(in); got != in {
				t.Errorf("line(%q) = %q, want it unchanged", in, got)
			}
		}
		if got := off.wrap(ansiRed, "Error:"); got != "Error:" {
			t.Errorf("wrap = %q, want it unchanged", got)
		}
	})
}
