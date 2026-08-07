[doc("Display available commands")]
default:
    @just --list

[doc("Build the binary")]
build:
    go build -o http-assert .

[doc("Build for release (optimized)")]
build-release:
    go build -ldflags="-s -w" -o http-assert .

[doc("Run all pre-commit checks")]
pre-commit: build tidy-check lint-config-check vet lint test test-race security

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