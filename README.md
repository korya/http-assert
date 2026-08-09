# http-assert ![Github Actions](https://github.com/korya/http-assert/actions/workflows/build.yml/badge.svg) [![Go Reference](https://pkg.go.dev/badge/github.com/korya/http-assert.svg)](https://pkg.go.dev/github.com/korya/http-assert)

`http-assert` is `curl` with assertions: the simplest way to test a server end
to end, or to wait for a service to come up ready.

```console
$ http-assert --retry 30 --retry-delay 1s \
    --assert-status 200 \
    --assert-jq '.status == "healthy"' \
    https://api.example.com/health
[.] HTTP/1.1 GET https://api.example.com/health
[:] HTTP/1.1 503 Service Unavailable
[-] FAILED 12ms

[~] retry 1/30 in 1s
[.] HTTP/1.1 GET https://api.example.com/health
[:] HTTP/1.1 200 OK
[+] PASSED 9ms
```

Every log line starts with a prefix:

- `[.]` the request going out
- `[:]` the response coming back
- `[>]` a redirect being followed
- `[~]` a wait before the next attempt
- `[+]` / `[-]` the verdict

## Why

Every pipeline has a step shaped like "deploy, then ask: is it actually up?"
The stock answers each see half the picture:

- **`curl -f`** checks the status code and nothing else. A health endpoint
  answering `200` with `{"status":"degraded"}` passes.
- **`curl -s … | jq -e`** checks the body and nothing else. A `503` whose body
  still says `{"status":"ok"}` passes, because `jq` never sees the status code.
- **Chaining both** closes those holes and opens others: the only diagnostic is
  a bare exit code, nothing retries while the service comes up, and the
  healthcheck image now needs `curl` *and* `jq` in it.

`http-assert` closes that gap with one static binary. It makes the request,
checks status, headers and body in one pass, and retries until the service is
ready. When a check fails, it reports every failing assertion with what it
actually saw:

```console
$ http-assert --assert-status 200 --assert-jq '.status == "healthy"' https://api.example.com/health

Error: 2 assertions failed:
- status: expected 200, got 503 ("503 Service Unavailable")
- jq[.status == "healthy"]: expected true, got "degraded"
```

Built for the places where the exit code is the whole point:

- **Deploy gates**: replace `sleep 10; curl -f` with a poll that passes the
  moment the service is ready and fails with evidence when it never is
- **End-to-end tests**: send the real request with `-X`, `-H` and `-d`, and
  assert on everything that comes back
- **Container healthchecks**: statically linked, drops into a `scratch` or
  `distroless` image with `COPY --from`
- **Monitoring probes**: reports that name every failed check and stay
  readable in a plain CI log
- **Backend verification**: `--maphost` aims the same request at a specific
  backend behind a load balancer

The flags follow `curl` where that helps; where a checking tool needs a
different answer, the deviation is deliberate, and
[Coming from curl](#coming-from-curl) lists every one.

## Contents

- [Installation](#installation)
- [Usage](#usage): [request options](#request-options),
  [assertions](#assertion-options), [JSON](#json-assertions),
  [redirects](#redirects), [retries](#retries), [compression](#compression),
  [logging](#logging-options)
- [Recipes](#recipes)
- [Reference](#reference): [environment variables](#environment-variables),
  [exit codes](#exit-codes), [coming from curl](#coming-from-curl)
- [License](#license) · [Development](#development)

## Installation

### Download a Release Binary

No Go toolchain required. Static binaries for Linux, macOS and Windows on
amd64 and arm64 are attached to every [release](https://github.com/korya/http-assert/releases).

```bash
# Pick the latest tag from https://github.com/korya/http-assert/releases
VERSION=v0.1.0
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
ARCHIVE="http-assert_${VERSION#v}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/korya/http-assert/releases/download/$VERSION"

curl -sSLO "$BASE/$ARCHIVE"
curl -sSLO "$BASE/checksums.txt"
sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 -c

tar xzf "$ARCHIVE" http-assert
```

The binary is statically linked, so it drops straight into a `scratch` or
`distroless` image with `COPY --from`.

### Shell Completion

Completion scripts for bash, zsh, fish and PowerShell are built in:

```bash
http-assert completion zsh --help   # per-shell install instructions
```

### From Source

```bash
go install github.com/korya/http-assert@latest
```

## Usage

### Basic Syntax

```bash
http-assert [flags] <URL>
```

At least one `--assert-*` flag is required; a run with no assertions exits `71`
rather than reporting a success it never checked.

### Request Options

| Flag | Short | Description |
|------|-------|-------------|
| `--request` | `-X` | HTTP method (default: GET, or POST when `-d` is given) |
| `--data` | `-d` | Request body data; implies POST and `Content-Type: application/x-www-form-urlencoded` unless overridden |
| `--header` | `-H` | Set request headers as `name: value` (can be used multiple times) |
| `--max-time` | `-m` | Request timeout in seconds (default: 20) |
| `--insecure` | `-k` | Skip SSL certificate verification |
| `--maphost` | | Map hostname:port to different destination |
| `--location` | `-L` | Follow redirects (see [Redirects](#redirects)) |
| `--max-redirs` | | Maximum redirects to follow with `-L` (default: 10) |
| `--retry` | | Retry a failed attempt this many times (see [Retries](#retries)) |
| `--retry-delay` | | Delay between attempts (default: 1s) |
| `--retry-max-time` | | Stop retrying after this long (default: no limit) |

A `-H` value needs a colon. A bare name exits `71` rather than being sent as a header with an empty value, which is what `curl` reads as "remove this header", so the two would have meant opposite things. Write `-H 'X-Foo:'` when an empty value is what you want.

`-d` follows `curl`: the method becomes POST unless `-X` says otherwise, and `Content-Type: application/x-www-form-urlencoded` is set unless a `-H` provides one (`-H 'Content-Type:'` counts as providing one). Two deviations remain: `-d @file` sends the literal string `@file` rather than reading the file, and a repeated `-d` is rejected rather than joined with `&`.

`--max-time` takes whole seconds; the three `--retry-*` options take durations with a unit (`1s`, `250ms`, `2m`). Requests use HTTP/1.1; HTTP/2 is never attempted.

### Assertion Options

| Flag | Description |
|------|-------------|
| `--assert-ok` | Assert the status is not an error (2xx or 3xx) |
| `--assert-status` | Assert specific status code |
| `--assert-header` | Assert header matches regex pattern; a name alone asserts presence |
| `--assert-header-eq` | Assert header equals exact value; a name alone asserts presence |
| `--assert-header-missing` | Assert header is not present |
| `--assert-body` | Assert body matches regex pattern |
| `--assert-body-eq` | Assert body equals exact value |
| `--assert-body-empty` | Assert body is empty |
| `--assert-jq` | Assert a jq expression yields `true` (can be used multiple times) |
| `--assert-redirect` | Assert redirect location matches regex |
| `--assert-redirect-eq` | Assert redirect location equals exact value |

The three header flags and `--assert-jq` can be repeated to make several assertions of that kind. Every other assertion flag takes a single value; giving one twice exits `71` rather than silently keeping the last.

`--assert-ok` and `--assert-body-empty` can be negated with `=false`, which asserts the opposite rather than cancelling the flag:

```bash
# Assert the endpoint IS failing -- useful for testing that a guard rejects
http-assert --assert-ok=false https://api.example.com/forbidden

# Assert something came back, without saying what
http-assert --assert-body-empty=false https://api.example.com/report
```

The three body assertions run against the decoded payload, never the bytes on the wire; see [Compression](#compression).

### JSON Assertions

`--assert-jq` runs a [jq](https://jqlang.github.io/jq/) expression against the
response body and passes when it yields `true`:

```bash
http-assert --assert-jq '.status == "success"' https://api.example.com/health
```

**Prefer it over `--assert-body` for JSON.** A regex works on the serialized
text, so it breaks when the server adds a space, reorders keys or escapes a
character -- none of which change what the response means:

```bash
# breaks on {"status": "success"}
http-assert --assert-body '"status":"success"' https://api.example.com/

# does not care how the JSON is formatted
http-assert --assert-jq '.status == "success"' https://api.example.com/
```

**Repeat it to assert several things**, alongside any other assertion. Every
failure is reported, not just the first:

```bash
http-assert \
  --assert-jq '.status == "success"' \
  --assert-jq '.users | length > 0' \
  --assert-jq '[.users[].active] | all' \
  --assert-status 200 \
  https://api.example.com/users
```

The expression yields the verdict itself rather than a path and an expected
value, so jq's types, comparisons, `select`, `length` and `test()` are all
available and there is no separate syntax to learn. An expression that works in
`jq` works here.

Three things to know:

- **A query that yields no output fails.** `.users[] | select(.id == 99) | .active`
  produces nothing when no user has that id, and passing would mean reporting
  success for a check that examined nothing.
- **A query must yield `true`, not a value.** `--assert-jq '.status'` fails with
  `expected true, got "success"`. Write the comparison.
- **A broken expression exits `71` before the request is made**, so a typo is
  never mistaken for a service answering wrongly.

A body that is not JSON, is empty, or is still compressed fails the assertion
saying which, rather than blaming the expression.

### Redirects

Redirects are not followed by default. A 3xx is delivered to the assertions
exactly as it arrived, which is what makes `--assert-redirect` and
`--assert-redirect-eq` possible at all -- a followed redirect has no `Location`
header left to assert on.

`--assert-ok` treats a 3xx as success, so a redirecting endpoint passes a
health check without the resource behind the redirect ever being fetched:

```console
$ http-assert --assert-ok https://old-domain.com/health
[.] HTTP/1.1 GET https://old-domain.com/health
[:] HTTP/1.1 301 Moved Permanently
[+] PASSED 84ms
```

Assert the status you actually mean (`--assert-status 200`) when that is not
what you wanted.

`-L` follows the chain instead, and every assertion then applies to the
response at the end of it:

```console
$ http-assert -L --assert-status 200 https://old-domain.com/health
[.] HTTP/1.1 GET https://old-domain.com/health
[>] 1 GET https://new-domain.com/health
[:] HTTP/1.1 200 OK
[+] PASSED 121ms
```

`--max-redirs` bounds the chain and defaults to 10. `--max-redirs 0` refuses
every redirect, as in `curl`. Exceeding the bound exits `92` and says so
explicitly, rather than reporting a network failure that did not happen. The
option needs `-L` to mean anything, so passing it alone exits `71` rather than
being quietly ignored.

`-L` cannot be combined with `--assert-redirect` or `--assert-redirect-eq`
(exit `71`). Following the redirect consumes the 3xx those two exist to
inspect, so the combination has no reading in which both flags get what they
asked for.

Two notes on following:

- A `302` on a `POST` is rewritten to a `GET` with no body, per the HTTP
  specification. Use `307` or `308` to preserve the method and body.
- `Authorization` and `Cookie` headers set with `-H` are dropped when a hop
  leaves the original domain. Redirects after the first are chosen by the
  server, which is why following is opt-in.

### Retries

The request is made once by default. `--retry` re-sends it after a failed
attempt, waiting `--retry-delay` in between, and stops at the first attempt
that passes:

```console
$ http-assert --retry 5 --retry-delay 1s --assert-ok https://api.example.com/health
[.] HTTP/1.1 GET https://api.example.com/health
[:] HTTP/1.1 503 Service Unavailable
[-] FAILED 3ms

[~] retry 1/5 in 1s
[.] HTTP/1.1 GET https://api.example.com/health
[:] HTTP/1.1 200 OK
[+] PASSED 4ms
```

**Any failure is retried** -- an unreachable host and a wrong answer alike. The
case this exists for is waiting for a service to come up, and there the response
usually arrives perfectly well and says the wrong thing, so retrying only the
connection failures would miss the point. `curl` draws the line differently.

**The delay is fixed, not exponential.** `--retry 30 --retry-delay 1s` reads as
"poll once a second for half a minute", and the worst case can be worked out
without a calculator. Sub-second values are allowed: `--retry-delay 250ms`.

**`--max-time` bounds each attempt, not the run.** Both are needed, and the
worst case is `retry x (max-time + retry-delay) + max-time` -- for `--retry 30`
at the defaults, over ten minutes. `--retry-max-time` bounds the run instead:

```console
$ http-assert --retry 100 --retry-delay 5s --retry-max-time 30s --assert-ok https://api.example.com/health
...
Error: gave up after 6 attempts (--retry-max-time is 30s):
```

The budget is checked before each retry rather than enforced mid-request, as in
`curl`, so an attempt already in flight can overrun it by up to one
`--max-time`. `--retry-max-time 0` -- the default -- means no budget, leaving
`--retry` as the only bound.

The failure names how many attempts were made. A log showing one failure and no
sign of the other five reads as a service that was never up, rather than one
that never came up.

Two combinations are refused with exit `71` rather than quietly ignored:
`--retry-delay` or `--retry-max-time` without `--retry` (a value nobody will
read), and a negative value for any of the three. `--retry-delay=-2s` is a
well-formed duration but would turn the pause between attempts into no pause at
all, so it is refused rather than obeyed. `--retry 0` is legal and means "make
the request once", so `--retry ${RETRIES:-0} --retry-delay 1s` works.

Note that the durations take a unit: `--retry-delay 5` is rejected, `5s` is not.

### Compression

A compressed body is decoded before the assertions run, so the body assertions
always see the payload:

```console
$ http-assert -H 'Accept-Encoding: gzip' --assert-body '"status":"success"' https://api.example.com/
[.] HTTP/1.1 GET https://api.example.com/
[:] HTTP/1.1 200 OK
[+] PASSED 41ms
```

`gzip` and `deflate` are understood, the latter in both the zlib form the RFC
specifies and the raw form much of the web actually sends.

**The response headers are reported exactly as they arrived.** Nothing is
deleted on the way, so a run can assert on the body *and* on how it was
encoded:

```bash
http-assert \
  -H 'Accept-Encoding: gzip' \
  --assert-header-eq "Content-Encoding: gzip" \
  --assert-body-eq '{"status":"success"}' \
  https://api.example.com/
```

**Nothing is advertised in `Accept-Encoding` unless you ask for it.** A server
that compresses only on request will answer in plain; ask with
`-H 'Accept-Encoding: gzip'` when you want to exercise content negotiation.

**An encoding with no decoder here fails only the body assertions**, naming
itself, and leaves the rest of the run intact:

```console
$ http-assert --assert-body '"status":"success"' https://cdn.example.com/
- body: response is br-encoded and was not decoded: no decoder for "br"; gzip and deflate are supported

$ http-assert --assert-ok https://cdn.example.com/
[+] PASSED 38ms
```

Brotli (`br`) and zstd are the encodings this covers in practice. Neither is
supported yet: [#77](https://github.com/korya/http-assert/issues/77) tracks
brotli and [#78](https://github.com/korya/http-assert/issues/78) tracks zstd.

A header that claims an encoding the body does not have is treated the same
way. Reading those bytes as plain text would be its own silent corruption.

### Logging Options

| Flag | Short | Description |
|------|-------|-------------|
| `--verbose` | `-v` | Enable verbose logging |
| `--silent` | `-s` | Only log errors |
| `--log-level` | | Set log level (debug, info, warn, error) |

The three options set one level. When more than one asks, the command line
beats the environment as a whole — so `-v` on a CI step overrides an
`HTTP_ASSERT_SILENT` from the job template — and within one channel `-s`
beats `-v` beats `--log-level`. A conflict never fails the run; each option
that lost to a different level is announced with a one-line
`Warning: -s overrides -v` on stderr, printed even when the winner is the
silencing flag.

**Everything the tool prints goes to stderr; stdout is always empty.** Use
`2>&1` when capturing output in a file or a pipe.

`warn` is accepted but currently logs exactly what `error` does; nothing in the
tool logs at the warn level.

### Other Options

| Flag | Short | Description |
|------|-------|-------------|
| `--version` | | Print the version, commit and build platform, then exit |
| `--help` | `-h` | Print usage, then exit |

```console
$ http-assert --version
http-assert version v0.1.0 (commit 4ffe282, built 2026-08-07T22:24:59Z, go1.25.5, linux/amd64)
```

A binary built from a checkout rather than a release reports the commit it was
built from instead of a tag.

## Recipes

### Deploy Gate

```bash
#!/bin/bash
# Deploy and validate service
deploy-service.sh

# Wait for the service to come up, then validate the deployment. Retrying
# replaces a fixed `sleep`: it returns as soon as the service is ready instead
# of always waiting for the worst case, and it does not give up early when the
# worst case is exceeded.
http-assert \
  --max-time 30 \
  --retry 30 --retry-delay 1s \
  --assert-ok \
  --assert-header-eq "X-Service-Version: $EXPECTED_VERSION" \
  https://api.example.com/health

if [ $? -eq 0 ]; then
  echo "Deployment validation passed"
else
  echo "Deployment validation failed"
  exit 1
fi
```

### Monitoring Script

```bash
#!/bin/bash
# Simple monitoring script
ENDPOINTS=(
  "https://api.example.com/health"
  "https://db.example.com/ping"
  "https://cache.example.com/status"
)

for endpoint in "${ENDPOINTS[@]}"; do
  # Two retries so a single blip does not page anyone at 3am.
  if http-assert --silent --retry 2 --retry-delay 5s --assert-ok "$endpoint"; then
    echo "✓ $endpoint"
  else
    echo "✗ $endpoint"
  fi
done
```

### Checking Backends Behind a Load Balancer

```bash
# Map requests to specific backend servers
http-assert \
  --maphost "api.example.com:443=backend1.internal:8443" \
  --assert-ok \
  https://api.example.com/health

# Test multiple backends
http-assert \
  --maphost "*:80=192.168.1.10" \
  --assert-status 200 \
  http://loadbalancer.example.com
```


```bash
# Test all backend servers through load balancer
BACKENDS=("backend1.internal" "backend2.internal" "backend3.internal")

for backend in "${BACKENDS[@]}"; do
  echo "Testing $backend..."
  http-assert \
    --maphost "api.example.com:443=$backend:8443" \
    --assert-ok \
    --assert-header "X-Backend-Server: $backend" \
    https://api.example.com/health
done
```

### POST with a JSON Body

```bash
# POST with JSON data; -d implies POST
http-assert \
  -H "Content-Type: application/json" \
  -d '{"username":"test","password":"secret"}' \
  --assert-status 201 \
  https://api.example.com/login
```

### Header Validation

```bash
# Assert specific headers are present and have expected values
http-assert \
  --assert-header-eq "X-API-Version: v1" \
  --assert-header-missing "X-Debug-Info" \
  --assert-header "Cache-Control: max-age=\d+" \
  https://api.example.com/data
```

## Reference

### Environment Variables

Six options can be set through the environment, using the `HTTP_ASSERT_` prefix with dashes replaced by underscores:

| Variable | Equivalent flag |
|----------|-----------------|
| `HTTP_ASSERT_VERBOSE` | `--verbose` |
| `HTTP_ASSERT_SILENT` | `--silent` |
| `HTTP_ASSERT_LOG_LEVEL` | `--log-level` |
| `HTTP_ASSERT_INSECURE` | `--insecure` |
| `HTTP_ASSERT_MAX_TIME` | `--max-time` |
| `HTTP_ASSERT_MAPHOST` | `--maphost` |

```bash
export HTTP_ASSERT_VERBOSE=true
export HTTP_ASSERT_MAX_TIME=30
export HTTP_ASSERT_INSECURE=true

http-assert --assert-ok https://api.example.com
```

**Every option not in that table is command-line only.** `--request`, `--header`, `--data`, `--location`, `--max-redirs`, the three `--retry*` options and every `--assert-*` flag ignore the environment; setting `HTTP_ASSERT_REQUEST=POST` or `HTTP_ASSERT_RETRY=5` has no effect.

**A command-line flag always wins over the environment**, which in turn wins over the built-in default. An empty variable counts as unset.

#### Proxies

`HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are honoured for the request itself. There is no flag for them, and no way to disable the behaviour from the command line: if one of these is set in your environment for unrelated reasons, requests go through it.

**A value that does not parse is rejected** rather than ignored, so a typo cannot silently change behaviour:

```console
$ HTTP_ASSERT_MAX_TIME=abc http-assert --assert-ok https://api.example.com
Error: Invalid value for HTTP_ASSERT_MAX_TIME="abc": strconv.ParseInt: parsing "abc": invalid syntax
$ echo $?
71
```

Booleans accept the Go forms (`true`, `false`, `1`, `0`, `t`, `f`, and their capitalisations); shell-style `yes`/`on` are rejected.

**`HTTP_ASSERT_MAPHOST` separates multiple mappings with whitespace, not commas:**

```bash
# Two mappings
export HTTP_ASSERT_MAPHOST="api.example.com:443=backend1:8443 api.example.com:80=backend1:8080"

# NOT a list -- parsed as one malformed mapping, exits 71
export HTTP_ASSERT_MAPHOST="api.example.com:443=backend1:8443,api.example.com:80=backend1:8080"
```

Repeating `--maphost` on the command line accumulates as usual.

### Exit Codes

The code answers whose fault the failure is — the invocation, the transport,
or the response:

- `0`: All assertions passed, or `--version`/`--help` was requested
- `71`: The invocation was rejected — a bad flag, value, combination or
  argument count — and no request was attempted
- `92`: The request produced no usable response (unreachable host, TLS
  failure, timeout, redirect bound exceeded)
- `93`: A response arrived, and at least one assertion failed

Before v0.2 there were five codes: `91` (unbuildable request) and `103`
(argument/flag syntax) are now `71`, and transport failures moved from `93`
to `92`, so `93` now always means "the service answered wrongly".

### Coming from curl

The flags follow `curl`'s names and semantics wherever that helps. The
deviations are deliberate, each one a place where `curl`'s answer is wrong for
a tool whose job is checking. This is the complete list:

| | `curl` | `http-assert` |
|---|---|---|
| Redirects | not followed without `-L` | same default; `-L` additionally refuses to combine with `--assert-redirect*`, which need the 3xx it consumes |
| `-H 'Name'` with no colon | removes the header | rejected, exit `71`; write `-H 'Name:'` to send an empty value |
| `-d @file` | reads the file | sends the literal string `@file` |
| `-d` repeated | values joined with `&` | rejected, exit `71` |
| `--retry` | transport errors and a fixed set of transient statuses, exponential backoff | any failed attempt, assertion failures included, fixed delay |
| Response decompression | opt-in via `--compressed` | always: gzip and deflate are decoded before assertions run |
| Pointing at a backend | `--resolve host:port:addr` takes an address | `--maphost 'host:port=dst[:port]'` takes a hostname or an address |

## License

`http-assert` is free software, licensed under the GNU General Public License
v3.0; see [LICENSE](LICENSE).

## Development

### Build from Repository

```bash
git clone https://github.com/korya/http-assert.git
cd http-assert
go build -o http-assert .
```

### Working on the Code

[`just`](https://github.com/casey/just) is the task runner, and CI runs the
same recipes:

```bash
just pre-commit   # build, tidy-check, vet, lint, gosec, unit tests, race
just test-e2e     # end-to-end suite: builds the CLI and drives it as a subprocess
just pre-push     # everything CI runs, including the end-to-end suite
just test-cover   # merged unit + end-to-end coverage
```

The end-to-end tests are opt-in: `go test ./...` runs the unit tests only, and
`-e2e` (or the recipes above) switches the full suite on.
