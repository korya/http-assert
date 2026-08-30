package main

import (
	"testing"

	"github.com/spf13/pflag"
)

func Test_collects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Type string
		Want bool
	}{
		{"string", false},
		{"int", false},
		{"bool", false},
		{"stringArray", true},
		{"stringSlice", true},
		{"intSlice", true},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.Type, func(t *testing.T) {
			if got := collects(tc.Type); got != tc.Want {
				t.Errorf("collects(%q) = %v, want %v", tc.Type, got, tc.Want)
			}
		})
	}
}

// Test_singleValue checks the wrapper stays invisible to everything except the
// count. pflag reads a flag back through String() and dispatches on Type(), so
// a wrapper that got either wrong would break --help and GetInt rather than
// catching a repeat.
func Test_singleValue(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("n", 7, "")
	v := &singleValue{inner: fs.Lookup("n").Value}
	fs.Lookup("n").Value = v

	if got, want := v.String(), "7"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := v.Type(), "int"; got != want {
		t.Errorf("Type() = %q, want %q", got, want)
	}
	if v.count != 0 {
		t.Errorf("count = %d before any assignment, want 0", v.count)
	}

	if err := fs.Parse([]string{"--n", "1", "--n", "2"}); err != nil {
		t.Fatalf("parse: %s", err)
	}

	if v.count != 2 {
		t.Errorf("count = %d after two occurrences, want 2", v.count)
	}
	if got, want := v.String(), "2"; got != want {
		t.Errorf("String() = %q, want %q -- the wrapper must not disturb the value", got, want)
	}

	// The reason the wrapper exists: pflag's own record cannot tell these apart.
	if !fs.Changed("n") {
		t.Error("Changed() = false after two occurrences")
	}

	// And the value is still readable through pflag's typed accessor, which
	// dispatches on Type() and parses String().
	if got, err := fs.GetInt("n"); err != nil || got != 2 {
		t.Errorf("GetInt() = %d, %v; want 2, nil", got, err)
	}
}

func Test_rejectRepeats(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("assert-status", "", "")
	fs.String("assert-body", "", "")
	fs.Bool("assert-ok", false, "")
	fs.StringArray("assert-header", nil, "")
	fs.String("request", "GET", "")

	rejectRepeats(fs)

	tests := []struct {
		Name    string
		Wrapped bool
		Why     string
	}{
		{"assert-status", true, "single-valued assertion"},
		{"assert-body", true, "single-valued assertion"},
		{"assert-ok", true, "a repeated bool flips the assertion rather than adding one"},
		{"assert-header", false, "repeating it is how you add assertions"},
		{"request", false, "not an assertion; last-wins is conventional for configuration"},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			_, wrapped := fs.Lookup(tc.Name).Value.(*singleValue)
			if wrapped != tc.Wrapped {
				t.Errorf("--%s wrapped = %v, want %v (%s)", tc.Name, wrapped, tc.Wrapped, tc.Why)
			}
		})
	}
}

// Test_checkRepeats_allows covers the path that must not terminate. The path
// that does call dief exits the process, so it is exercised by the end-to-end
// suite against the real binary instead.
func Test_checkRepeats_allows(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("assert-status", "", "")
	fs.StringArray("assert-header", nil, "")
	rejectRepeats(fs)

	if err := fs.Parse([]string{"--assert-status", "200", "--assert-header", "a", "--assert-header", "b"}); err != nil {
		t.Fatalf("parse: %s", err)
	}

	checkRepeats(fs) // must return; a repeated collecting flag is legitimate
}
