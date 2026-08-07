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
pre-commit: build vet lint test test-race test-cover security

[doc("Build and check compilation without creating binary")]
check:
    go build -o /dev/null .

[doc("Run go vet")]
vet:
    go vet ./...

[doc("Run golangci-lint")]
lint:
    golangci-lint run ./...

[doc("Run tests")]
test:
    go test ./...

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
    E2E_COVERDIR="$e2e" go test ./... -cover -args -test.gocoverdir="$unit"
    go tool covdata percent -i="$unit,$e2e"

[doc("Run tests with coverage and generate HTML report")]
test-coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    unit=$(mktemp -d); e2e=$(mktemp -d)
    trap 'rm -rf "$unit" "$e2e"' EXIT
    E2E_COVERDIR="$e2e" go test ./... -cover -args -test.gocoverdir="$unit"
    go tool covdata textfmt -i="$unit,$e2e" -o=coverage.out
    go tool cover -html=coverage.out -o coverage.html
    echo "Coverage report: coverage.html"

[doc("Show per-function coverage, lowest first")]
test-cover-func:
    #!/usr/bin/env bash
    set -euo pipefail
    unit=$(mktemp -d); e2e=$(mktemp -d)
    trap 'rm -rf "$unit" "$e2e"' EXIT
    E2E_COVERDIR="$e2e" go test ./... -cover -args -test.gocoverdir="$unit" >/dev/null
    go tool covdata func -i="$unit,$e2e" | sort -k2 -n

[doc("Clean build artifacts")]
clean:
    rm -f http-assert coverage.out coverage.html

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