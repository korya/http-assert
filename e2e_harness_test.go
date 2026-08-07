package main_test

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The end-to-end suite drives the compiled binary as a subprocess and asserts on
// its externally observable contract only: exit code, stdout, stderr.
//
// It deliberately avoids reaching into package internals. Every test here must
// keep passing verbatim across the planned rearchitecture (#54 viper removal,
// #55 run() extraction, #56 assertion constructors), which is the property that
// makes those refactors safe to perform.

// runE2E gates the whole suite. It is opt-in: `go test ./...` runs the unit
// tests only, and the end-to-end tests run when explicitly asked for.
//
// A registered flag is used rather than a build tag on purpose. A tagged file
// is invisible to `go vet` and `golangci-lint` unless every invocation passes
// the tag, so tagging would silently drop this file out of both -- verified,
// and the reason the switch lives here instead.
var runE2E = flag.Bool("e2e", false, "run the end-to-end suite (builds and executes the CLI)")

var (
	binPath  string
	buildErr error
	buildOne sync.Once
)

// coverDir collects coverage counters emitted by the subprocesses. `just
// test-cover` sets E2E_COVERDIR and merges the result with the unit-test
// profile; when it is unset the suite still runs, just without instrumentation.
func coverDir() string { return os.Getenv("E2E_COVERDIR") }

// binary compiles the CLI once per test run and returns its path. It is built
// with -cover whenever E2E_COVERDIR is set so that subprocess execution counts
// toward the reported coverage.
func binary(t *testing.T) string {
	t.Helper()

	if !*runE2E {
		t.Skip("e2e: pass -e2e to run (or use `just test-e2e`)")
	}

	buildOne.Do(func() {
		dir, err := os.MkdirTemp("", "http-assert-e2e")
		if err != nil {
			buildErr = err
			return
		}

		bin := filepath.Join(dir, "http-assert")
		if isWindows() {
			bin += ".exe"
		}

		args := []string{"build"}
		if coverDir() != "" {
			args = append(args, "-cover")
		}
		args = append(args, "-o", bin, ".")

		cmd := exec.Command("go", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, out)
			return
		}
		binPath = bin
	})

	if buildErr != nil {
		t.Fatalf("cannot build the CLI under test: %s", buildErr)
	}

	return binPath
}

func isWindows() bool { return os.PathSeparator == '\\' }

// result is everything a caller of the CLI can observe.
type result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Output is stdout and stderr concatenated, for assertions that do not care
// which stream carried the text.
func (r result) Output() string { return r.Stdout + r.Stderr }

// run executes the CLI with the given environment overlay and arguments.
//
// The inherited environment is scrubbed of every variable that could change the
// result -- HTTP_ASSERT_* from a developer's shell, and the proxy variables that
// http.ProxyFromEnvironment honours -- so a test asserts on what it sets and
// nothing else. env entries are applied on top of that clean base; a nil map
// means "no environment configuration at all".
func run(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()

	cmd := exec.Command(binary(t), args...)
	cmd.Env = testEnv(env)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := result{Stdout: stdout.String(), Stderr: stderr.String()}
	switch e := err.(type) {
	case nil:
		res.ExitCode = 0
	case *exec.ExitError:
		res.ExitCode = e.ExitCode()
	default:
		t.Fatalf("cannot run the CLI: %s", err)
	}

	return res
}

func testEnv(overlay map[string]string) []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "HTTP_ASSERT_") || isProxyVar(k) {
			continue
		}
		out = append(out, kv)
	}

	if d := coverDir(); d != "" {
		out = append(out, "GOCOVERDIR="+d)
	}

	for k, v := range overlay {
		out = append(out, k+"="+v)
	}

	return out
}

func isProxyVar(name string) bool {
	switch strings.ToLower(name) {
	case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
		return true
	}
	return false
}

// Exit codes the CLI is contracted to return. Nothing in the repo documented
// these before this suite existed; see #24.
const (
	exitOK          = 0   // every assertion passed
	exitBadFlagVal  = 71  // a flag value failed to parse (--log-level, --maphost)
	exitBadRequest  = 91  // the request could not be constructed (-X, URL)
	exitRequestFail = 93  // transport failure, or at least one assertion failed
	exitUsage       = 103 // wrong argument count, unknown flag
)

// assertExit fails the test with full context when the exit code is unexpected.
// Printing both streams matters: a wrong code is almost always explained by
// output the assertion itself does not look at.
func assertExit(t *testing.T, got result, want int) {
	t.Helper()
	if got.ExitCode != want {
		t.Fatalf("exit code = %d, want %d\n--- stdout ---\n%s\n--- stderr ---\n%s",
			got.ExitCode, want, got.Stdout, got.Stderr)
	}
}

// characterizes marks a test as pinning behavior that is currently *wrong* and
// tracked by the given GitHub issue. It is not a skip: the assertion still runs
// and must still pass. When the issue is fixed the test fails, which is the
// signal to update the expectation here in the same commit.
//
// Tagging (rather than only commenting) makes the whole set selectable:
//
//	go test -run 'TestE2E.*/characterizes' ./...
func characterizes(t *testing.T, issue int, behavior string) {
	t.Helper()
	t.Logf("characterizes #%d: %s (expected to fail once the issue is fixed)", issue, behavior)
}

func assertContains(t *testing.T, got result, want string) {
	t.Helper()
	if !strings.Contains(got.Output(), want) {
		t.Fatalf("output does not contain %q\n--- stdout ---\n%s\n--- stderr ---\n%s",
			want, got.Stdout, got.Stderr)
	}
}

func assertNotContains(t *testing.T, got result, unwanted string) {
	t.Helper()
	if strings.Contains(got.Output(), unwanted) {
		t.Fatalf("output unexpectedly contains %q\n--- stdout ---\n%s\n--- stderr ---\n%s",
			unwanted, got.Stdout, got.Stderr)
	}
}
