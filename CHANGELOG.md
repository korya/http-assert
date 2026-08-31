# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the major version is 0, a minor bump may carry a breaking change; those
are marked **Breaking** and listed first in their section.

## [Unreleased]

## [0.4.0] - 2026-08-30

### Added

- A reusable Go package at `github.com/korya/http-assert` exposes the HTTP
  client, structured results and all response assertion constructors. The
  library invokes its configured HTTP client once and leaves retries, logging
  and presentation to its caller.
- Generic `ha.Must(...)` keeps values built from static input inline, including
  HTTP requests and assertions, while preserving explicit errors for runtime
  input.

### Changed

- **Breaking for source installs:** the CLI now lives at
  `github.com/korya/http-assert/cmd/http-assert`; use
  `go install github.com/korya/http-assert/cmd/http-assert@latest`. Published
  release archives and the `http-assert` binary name are unchanged.
- Assertion failures and evaluation errors are structured library data. Each
  outcome is exclusively a pass, a failed assertion or an evaluation error,
  and its assertion family is consistently typed. The CLI owns human-readable
  formatting and preserves its existing output.
- Assertion families use the exported `AssertionKind` type and constants
  instead of requiring consumers to compare raw strings.
- The library's zero-value client uses a 20-second total request timeout instead
  of the unbounded `http.DefaultClient`. Callers can still inject an HTTP client
  or apply a shorter request-context deadline.

### Fixed

- jq evaluation stops when its response request's context is cancelled while
  retaining the package's ten-second safety ceiling.

## [0.3.0] - 2026-08-11

### Added

- `--assert-status` accepts a status class (`2xx`), a comma-separated list, an
  inclusive range (`500-503`), or any mix of the three: `301,2xx,500-503` is one
  spec. An exact code behaves and reads exactly as before.
- Response bodies encoded with `br` or `zstd` are decoded before the body
  assertions run. Previously the three body assertions refused both by name,
  which is what a CDN in front of the endpoint actually serves.
- The verdict is coloured when stderr is a terminal, and the retry line is
  yellow rather than dim. Redirected output is unchanged.

### Changed

- A status spec that can never match is rejected at the flag with exit 71,
  rather than being accepted and failing an assertion at exit 93.
- Building from source now requires Go 1.26, and the build toolchain is pinned.
  Release binaries need no Go toolchain at all.

### Fixed

- A failed header assertion writes values the way a person writes them --
  `got "abc123"` for one, `got "first", "second"` for several -- instead of Go
  slice syntax. `--assert-header`, `--assert-header-eq` and
  `--assert-header-missing` were all affected. That a header assertion holds
  when *any* value matches is now documented in both `--help` and the README.

## [0.2.0] - 2026-08-09

### Changed

- **Breaking:** exit codes now answer whose fault the failure is. `71` means the
  invocation was rejected and no request was attempted, `92` means the request
  produced no usable response, and `93` means a response arrived and at least one
  assertion failed. Codes `91` and `103` are gone -- both are now `71` -- and
  transport failures moved from `93` to `92`. Scripts that only test for
  non-zero are unaffected; scripts that branch on a specific code must be
  updated.
- **Breaking:** `-d` implies `POST`, as in curl, and sets
  `Content-Type: application/x-www-form-urlencoded` when no `Content-Type`
  header is given. An explicit `-X` always wins, even `-X GET`. A script that
  passed `-d` and relied on the method staying `GET` changes behaviour.
- `--assert-body-empty=false` asserts the body is *not* empty, matching what
  `--assert-ok=false` already did. It previously asserted nothing, and the run
  died reporting that no assertion had been made.
- Verbosity conflicts resolve by an explicit priority instead of an undocumented
  ladder: the command line beats the environment, and within one channel `-s`
  beats `-v` beats `--log-level`. An overridden request is announced on stderr
  rather than vanishing. Invocations that never conflicted are byte-identical.
- A repeated assertion flag is rejected instead of silently dropping the earlier
  one. Assertion flags take a single value; `--assert-header` and `--assert-jq`
  accumulate by design.

### Added

- `--assert-jq` runs a jq expression against a JSON body and passes when it
  yields true, so an assertion survives reformatting, key reordering and
  escaping that a regex over the serialized text does not. Repeating the flag
  accumulates.
- `-L`/`--location` follows redirects and hands the final response to every
  assertion. `--max-redirs` bounds the chain, defaulting to 10. Not following
  remains the default, and `-L` is refused alongside `--assert-redirect` or
  `--assert-redirect-eq`, which need the 3xx that following consumes.
- `--retry` re-sends a failed check until it passes, with `--retry-delay` between
  attempts and `--retry-max-time` bounding the whole run. Any failure is
  retried, an unreachable host and a wrong answer alike, which is the case a
  deploy gate exists for.

### Fixed

- Response bodies are decoded before assertions run. Previously a compressed body
  reached the body assertions as raw bytes whenever net/http had not decoded it
  itself, so `--assert-body '.'` against a gzipped response reported `PASSED` for
  a check it had never made.
- `--assert-body-eq ''` and patterns that match the empty string (`^$`, `.*`)
  can pass. An emptiness guard meant to word the failure well had been deciding
  the verdict, so a 204 could not be asserted to have the body a 204 is defined
  to have.
- A `-H` value with no colon is rejected instead of accepted.
- The failure dump is formatted rather than serialized, whitespace-only bodies
  are rendered as text, and the request body is shown.

## [0.1.0] - 2026-08-07

First release under `github.com/korya/http-assert`, with published binaries.

### Added

- `--version` reports the release, the commit and the Go toolchain. Asking the
  binary what it was used to be an error.
- Static binaries for Linux, macOS and Windows on amd64 and arm64, with
  checksums, attached to every release. The binary is CGO-free, so it drops into
  a `scratch` or `distroless` image.

### Changed

- Environment configuration no longer goes through viper, which dropped 35 of
  the module's 38 non-stdlib dependencies. Exactly six options read the
  environment and the list is now explicit; previously the set was neither
  documented nor consistent, and `HTTP_ASSERT_REQUEST` silently did nothing
  while `HTTP_ASSERT_INSECURE` worked.
- An unparseable environment value is rejected with exit 71 and the parser's own
  reason. Previously it was applied as the type's zero value, so
  `HTTP_ASSERT_MAX_TIME=abc` disabled the request timeout entirely and
  `HTTP_ASSERT_VERBOSE=yes` quietly turned verbosity off.

### Fixed

- An unparseable regular expression in `--assert-body`, `--assert-header` or
  `--assert-redirect` is reported against the flag that carried it, instead of
  crashing with a Go stack trace and exit code 2.
- `printPayload` no longer panics on a negative crop size. Not reachable through
  the CLI; a latent defect rather than a live one.

## 0.0.7 and earlier

Releases up to and including 0.0.7 predate this changelog, the move to
Conventional Commits, and the transfer from `PlanitarInc`. See the
[git history](https://github.com/korya/http-assert/commits/v0.0.7) for that
period.

[Unreleased]: https://github.com/korya/http-assert/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/korya/http-assert/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/korya/http-assert/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/korya/http-assert/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/korya/http-assert/compare/v0.0.7...v0.1.0
