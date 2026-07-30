# go-health-checker

A small CLI tool that probes a list of HTTP endpoints defined in a JSON config
file and prints a JSON report with each endpoint's status code, latency, and
health. Exits non-zero when any endpoint is unhealthy, so it drops cleanly into
CI pipelines and cron jobs.

## Features

- Concurrent checks — all endpoints are probed in parallel, results stay in config order
- Latency measurement per endpoint (milliseconds)
- Health classification: 2xx/3xx = healthy, anything else (4xx/5xx, connection errors, timeouts) = unhealthy
- Aggregated summary (total / healthy / unhealthy)
- Exit codes: `0` all healthy, `1` at least one unhealthy, `2` config/usage error
- Zero external dependencies — Go standard library only

## Tech stack

- Go (1.22+), standard library only: `net/http`, `encoding/json`, `sync`, `flag`
- Tests use `net/http/httptest` — no network access required

## Architecture

```
.
├── main.go                  # CLI entry point: flags, config loading, JSON output, exit code
├── checker/
│   ├── config.go            # Config struct, JSON parsing, validation
│   ├── checker.go           # Concurrent probing + Report/Summary types
│   └── checker_test.go      # Unit tests (httptest servers, table-driven config tests)
├── config.example.json      # Sample configuration
└── go.mod
```

The `main` package is a thin shell: it parses flags, loads the config via
`checker.LoadConfig`, runs `Checker.CheckAll`, marshals the resulting `Report`
as indented JSON, and maps the summary to an exit code. All logic lives in the
`checker` package so it is independently testable and reusable as a library.

`CheckAll` launches one goroutine per endpoint and waits with a
`sync.WaitGroup`; each goroutine writes to its own pre-indexed slot in the
results slice, so output order matches the config and no mutex is needed.

## Setup

Requires Go 1.22 or newer.

```sh
git clone <this-repo>
cd go-health-checker
go build -o go-health-checker .
```

## Usage

```sh
# Use the sample config
cp config.example.json config.json
./go-health-checker -config config.json

# Override the per-request timeout from the CLI
./go-health-checker -config config.json -timeout 1s
```

### Config format

```json
{
  "timeout_ms": 2000,
  "endpoints": [
    { "name": "example", "url": "https://example.com" },
    { "name": "api", "url": "https://api.example.com/health" }
  ]
}
```

- `timeout_ms` — optional per-request timeout (default: 5000 ms; overridden by `-timeout`)
- `endpoints` — required, non-empty; each entry needs a `name` and an absolute `http(s)` URL

### Example output

```json
{
  "checked_at": "2026-07-30T08:06:10Z",
  "summary": { "total": 3, "healthy": 1, "unhealthy": 2 },
  "results": [
    {
      "name": "local-ok",
      "url": "http://127.0.0.1:8765/",
      "status_code": 200,
      "latency_ms": 3,
      "healthy": true
    },
    {
      "name": "local-missing",
      "url": "http://127.0.0.1:8765/nope",
      "status_code": 404,
      "latency_ms": 7,
      "healthy": false,
      "error": "unexpected status 404"
    }
  ]
}
```

### Exit codes

| Code | Meaning                        |
| ---- | ------------------------------ |
| 0    | All endpoints healthy          |
| 1    | At least one endpoint unhealthy |
| 2    | Config file error / bad usage  |

## Running tests

```sh
go test ./... -v
```

The suite covers config parsing and validation (table-driven), healthy/redirect/
error/timeout/refused-connection probing against local `httptest` servers,
result ordering, summary aggregation, and a concurrency sanity check.
