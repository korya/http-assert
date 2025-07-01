# http-assert ![Github Actions](https://github.com/PlanitarInc/http-assert/actions/workflows/build.yml/badge.svg) [![Go Reference](https://pkg.go.dev/badge/github.com/PlanitarInc/http-assert.svg)](https://pkg.go.dev/github.com/PlanitarInc/http-assert)

A command-line tool for performing HTTP requests and asserting properties of the response. This tool is designed for testing HTTP endpoints, health checks, monitoring, and CI/CD pipelines.

## Purpose

`http-assert` combines the functionality of making HTTP requests with the ability to validate responses against multiple criteria. It's particularly useful for:

- **Health checks and monitoring**: Verify that your APIs are returning expected responses
- **CI/CD pipelines**: Validate deployed services before proceeding with deployment
- **Integration testing**: Test HTTP endpoints with various assertion conditions
- **Load balancer testing**: Use host mapping to test different backend servers
- **SSL/TLS validation**: Test secure endpoints with certificate validation options

## Installation

### From Source

```bash
go install github.com/PlanitarInc/http-assert@latest
```

### Build from Repository

```bash
git clone https://github.com/PlanitarInc/http-assert.git
cd http-assert
go build -o http-assert .
```

## Usage

### Basic Syntax

```bash
http-assert [flags] <URL>
```

### Request Options

| Flag | Short | Description |
|------|-------|-------------|
| `--request` | `-X` | HTTP method (default: GET) |
| `--data` | `-d` | Request body data |
| `--header` | `-H` | Set request headers (can be used multiple times) |
| `--max-time` | `-m` | Request timeout in seconds (default: 20) |
| `--insecure` | `-k` | Skip SSL certificate verification |
| `--maphost` | | Map hostname:port to different destination |

### Assertion Options

| Flag | Description |
|------|-------------|
| `--assert-ok` | Assert 2xx status code |
| `--assert-status` | Assert specific status code |
| `--assert-header` | Assert header matches regex pattern |
| `--assert-header-eq` | Assert header equals exact value |
| `--assert-header-missing` | Assert header is not present |
| `--assert-body` | Assert body matches regex pattern |
| `--assert-body-eq` | Assert body equals exact value |
| `--assert-body-empty` | Assert body is empty |
| `--assert-redirect` | Assert redirect location matches regex |
| `--assert-redirect-eq` | Assert redirect location equals exact value |

### Logging Options

| Flag | Short | Description |
|------|-------|-------------|
| `--verbose` | `-v` | Enable verbose logging |
| `--silent` | `-s` | Only log errors |
| `--log-level` | | Set log level (debug, info, warn, error) |

## Examples

### Basic Health Check

```bash
# Simple health check - assert 200 OK
http-assert --assert-ok https://api.example.com/health
```

### POST Request with JSON Body

```bash
# POST with JSON data and assert specific status
http-assert -X POST \
  -H "Content-Type: application/json" \
  -d '{"username":"test","password":"secret"}' \
  --assert-status 201 \
  https://api.example.com/login
```

### Multiple Assertions

```bash
# Multiple assertions on the same request
http-assert \
  --assert-ok \
  --assert-header-eq "Content-Type: application/json" \
  --assert-body "\"status\":\"success\"" \
  https://api.example.com/status
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

### SSL and Security Testing

```bash
# Test with SSL verification disabled
http-assert --insecure --assert-ok https://self-signed.example.com

# Test with custom timeout
http-assert --max-time 5 --assert-ok https://slow-api.example.com
```

### Host Mapping for Load Balancer Testing

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

### Redirect Testing

```bash
# Assert redirect to specific URL
http-assert \
  --assert-redirect-eq "https://new-domain.com/path" \
  https://old-domain.com/path

# Assert redirect matches pattern
http-assert \
  --assert-redirect "https://.*\.example\.com/.*" \
  https://redirect.example.com

# Note: URLs with query parameters should be quoted to avoid shell interpretation
http-assert \
  --assert-redirect-eq "https://example.com/target" \
  "https://example.com/redirect?url=https://example.com/target"
```

### Body Content Validation

```bash
# Assert exact body content
http-assert \
  --assert-body-eq "OK" \
  https://api.example.com/ping

# Assert body matches regex pattern
http-assert \
  --assert-body "\"users\":\s*\[\]" \
  https://api.example.com/users

# Assert empty response body
http-assert \
  --assert-body-empty \
  https://api.example.com/delete-resource
```

### Environment Variables

You can also configure the tool using environment variables with the `HTTP_ASSERT_` prefix:

```bash
export HTTP_ASSERT_VERBOSE=true
export HTTP_ASSERT_MAX_TIME=30
export HTTP_ASSERT_INSECURE=true

http-assert --assert-ok https://api.example.com
```

### Exit Codes

- `0`: All assertions passed
- `93`: Failed to perform HTTP request or assertions failed
- `103`: Invalid command line arguments or other errors

## Use Cases

### CI/CD Pipeline Integration

```bash
#!/bin/bash
# Deploy and validate service
deploy-service.sh

# Wait for service to be ready
sleep 10

# Validate deployment
http-assert \
  --max-time 30 \
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
  if http-assert --silent --assert-ok "$endpoint"; then
    echo "✓ $endpoint"
  else
    echo "✗ $endpoint"
  fi
done
```

### Load Balancer Health Check

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
