[doc("Display available commands")]
default:
    @just --list

[doc("Build the binary")]
build:
    go build -o http-assert .

[doc("Build for release (optimized)")]
build-release:
    go build -ldflags="-s -w" -o http-assert .

[doc("Run every platform-independent check: build, tidy, config, vet, lint, security")]
static-checks: build tidy-check lint-config-check vet lint security

[doc("Run all pre-commit checks")]
pre-commit: static-checks test test-race

[doc("Build and check compilation without creating binary")]
check:
    go build -o /dev/null .

[doc("Fail if go.mod or go.sum is not tidy")]
tidy-check:
    go mod tidy -diff

[doc("Fail if the standard linters are no longer enabled")]
lint-config-check:
    #!/usr/bin/env bash
    # .golangci.yml is meant to ADD to the default linter set, never replace it.
    # Setting `linters.default: none` would silently drop the standard linters,
    # and nothing would fail -- fewer linters simply means fewer findings. This
    # asserts the set we expect to be active really is.
    set -euo pipefail
    enabled=$(golangci-lint linters | sed -n '/^Enabled/,/^Disabled/p' | grep -oE '^[a-z0-9]+' || true)
    # Distinguish "could not parse" from "the set really did shrink" -- both
    # must fail, but conflating them sends the next reader to the wrong file.
    if [ -z "$enabled" ]; then
      echo "cannot read the enabled linters from 'golangci-lint linters'; the parser is stale" >&2
      exit 1
    fi
    for l in errcheck govet ineffassign staticcheck unused forbidigo; do
      if ! echo "$enabled" | grep -qx "$l"; then
        echo "linter '$l' is no longer enabled -- check linters.default in .golangci.yml" >&2
        exit 1
      fi
    done

[doc("Warn if the pinned toolchain has fallen behind its patch line")]
toolchain-check:
    #!/usr/bin/env bash
    # `toolchain` in go.mod buys reproducible builds -- the Go version is
    # stamped into the binary, so a floating compiler changes the checksum that
    # -trimpath and mod_timestamp exist to keep stable. What it costs is the
    # automatic patch upgrade GOTOOLCHAIN=auto used to perform, and every Go
    # patch release carries security fixes. Nothing else bumps the pin, so this
    # says when it has fallen behind.
    #
    # It warns rather than fails. A new Go patch is not a defect in this
    # repository, and a red build every six weeks teaches everyone to ignore it.
    set -uo pipefail
    pinned=$(sed -n 's/^toolchain go//p' go.mod)
    if [ -z "$pinned" ]; then
      echo "go.mod has no toolchain directive; nothing to check"
      exit 0
    fi
    minor=${pinned%.*}
    latest=$(curl -fsS --max-time 10 "https://go.dev/dl/?mode=json&include=all" \
      | grep -oE "\"go${minor//./\\.}(\.[0-9]+)?\"" | tr -d '"' | sed 's/^go//' \
      | sort -uV | tail -1)
    # An unreachable go.dev is not a reason to fail a build that is otherwise
    # fine, and not a reason to claim the pin is current either.
    if [ -z "$latest" ]; then
      echo "could not read the Go release list; skipped the freshness check" >&2
      exit 0
    fi
    if [ "$pinned" = "$latest" ]; then
      echo "toolchain go${pinned} is the current ${minor} patch"
      exit 0
    fi
    msg="toolchain go${pinned} trails go${latest}; Go patch releases carry security fixes"
    [ -n "${GITHUB_ACTIONS:-}" ] && echo "::warning file=go.mod::${msg}"
    echo "$msg" >&2

[doc("Run go vet")]
vet:
    go vet ./...

[doc("Run golangci-lint")]
lint:
    golangci-lint run ./...

[doc("Run unit tests (fast; end-to-end tests are skipped)")]
test:
    go test ./...

[doc("Run the end-to-end suite (builds and executes the CLI)")]
test-e2e:
    go test ./... -e2e -count=1

[doc("Run tests with race detection")]
test-race:
    go test ./... -race

[doc("Run tests with coverage (unit + end-to-end, merged)")]
test-cover:
    #!/usr/bin/env bash
    # The end-to-end suite drives a separate binary, so its coverage arrives as
    # counter files rather than in the unit-test profile. Collect both into
    # covdata directories and merge, otherwise everything main.go does at
    # runtime reads as uncovered.
    set -euo pipefail
    unit=$(mktemp -d); e2e=$(mktemp -d)
    trap 'rm -rf "$unit" "$e2e"' EXIT
    E2E_COVERDIR="$e2e" go test ./... -e2e -count=1 -cover -args -test.gocoverdir="$unit"
    go tool covdata percent -i="$unit,$e2e"

[doc("Run tests with coverage and generate HTML report")]
test-coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    unit=$(mktemp -d); e2e=$(mktemp -d)
    trap 'rm -rf "$unit" "$e2e"' EXIT
    E2E_COVERDIR="$e2e" go test ./... -e2e -count=1 -cover -args -test.gocoverdir="$unit"
    go tool covdata textfmt -i="$unit,$e2e" -o=coverage.out
    go tool cover -html=coverage.out -o coverage.html
    echo "Coverage report: coverage.html"

[doc("Show per-function coverage, lowest first")]
test-cover-func:
    #!/usr/bin/env bash
    set -euo pipefail
    unit=$(mktemp -d); e2e=$(mktemp -d)
    trap 'rm -rf "$unit" "$e2e"' EXIT
    E2E_COVERDIR="$e2e" go test ./... -e2e -count=1 -cover -args -test.gocoverdir="$unit" >/dev/null
    go tool covdata func -i="$unit,$e2e" | sort -k2 -n

[doc("Clean build artifacts")]
clean:
    rm -rf http-assert coverage.out coverage.html dist

[doc("Install the binary to $GOPATH/bin")]
install:
    go install .

[doc("Format Go code")]
fmt:
    go fmt ./...

[doc("Update dependencies")]
deps-update:
    go get -u ./...
    go mod tidy

[doc("Download dependencies")]
deps-download:
    go mod download

[doc("Build the release artifacts locally without publishing anything")]
release-snapshot:
    # Exercises the whole release pipeline -- cross-compilation, archives,
    # checksums -- against the working tree. Publishes nothing and needs no
    # tag, so it is safe to run at any point.
    goreleaser release --snapshot --clean

[doc("Print the CHANGELOG.md section for a version, for use as release notes")]
release-notes version:
    #!/usr/bin/env bash
    # The release notes on GitHub are CHANGELOG.md and nothing else, so this is
    # what stands between a curated section and goreleaser's commit dump. It
    # prints to stdout; the caller decides where the file goes -- not dist/,
    # which `goreleaser release --clean` deletes before it reads anything.
    #
    # Failing here is the point. A tag whose section is missing or empty stops
    # the release before a single artifact is published, which is recoverable;
    # a published release with empty notes is not.
    set -euo pipefail
    v="{{ version }}"; v="${v#v}"
    section=$(awk -v v="$v" '
      BEGIN { gsub(/\./, "\\.", v) }
      $0 ~ "^## \\[" v "\\]" { p = 1; next }
      p && /^## /            { exit }
      p && /^\[[^]]+\]: /    { exit }
      p                      { line[++n] = $0; if (NF) last = n }
      END { for (i = 1; i <= last; i++) print line[i] }
    ' CHANGELOG.md | sed -e '/./,$!d')
    if [ -z "$section" ]; then
      echo "CHANGELOG.md has no entries under [$v]" >&2
      exit 1
    fi
    printf '%s\n' "$section"

[doc("Warn if a branch changes Go sources without touching CHANGELOG.md")]
changelog-check base="origin/master":
    #!/usr/bin/env bash
    # A changelog that only mentions some of the changes is worse than none,
    # because it still reads as the source of truth. Nothing else notices when
    # an entry is forgotten -- the release gate fires far too late, when the
    # tag is already cut.
    #
    # It warns rather than fails, like toolchain-check. Plenty of Go changes
    # are genuinely invisible to a user, and a red build for a refactor teaches
    # everyone to click through the one that matters.
    set -uo pipefail
    changed=$(git diff --name-only "{{ base }}...HEAD" 2>/dev/null || true)
    if [ -z "$changed" ]; then
      echo "no changes against {{ base }}; nothing to check"
      exit 0
    fi
    echo "$changed" | grep -q '\.go$' || exit 0
    echo "$changed" | grep -qx 'CHANGELOG.md' && exit 0
    msg="Go sources changed but CHANGELOG.md did not; add an entry under [Unreleased] if this is user-visible"
    [ -n "${GITHUB_ACTIONS:-}" ] && echo "::warning file=CHANGELOG.md::${msg}"
    echo "$msg" >&2

[doc("Run every check CI runs, including the end-to-end suite")]
pre-push: pre-commit test-e2e

[doc("Run a quick development cycle")]
dev: fmt vet test

[doc("Show Go version and environment")]
info:
    @echo "Go version:"
    @go version
    @echo "\nGo environment:"
    @go env GOOS GOARCH
    @echo "\nModule info:"
    @go list -m

[doc("Run security scan with gosec (if installed)")]
security:
    gosec ./...
