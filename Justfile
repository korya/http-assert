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
pre-commit: build vet lint test test-race test-cover

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

[doc("Run tests with coverage")]
test-cover:
    go test ./... -cover

[doc("Run tests with coverage and generate HTML report")]
test-coverage:
    go test ./... -coverprofile=coverage.out
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

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