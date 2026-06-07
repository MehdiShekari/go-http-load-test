# go-http-load-test

A simple yet professional HTTP load tester written in Go.  
It offers precise concurrency control, lock‑free metrics, custom headers/methods/body, and a latency histogram.

## Features

- **Lock‑free, high‑speed recording** – request latencies and error flags are stored in pre‑allocated slices using atomic slot allocation; no mutexes during measurement.
- **Worker pool with exact concurrency** – a channel semaphore limits simultaneous goroutines to exactly `-c`.
- **Flexible HTTP settings** – custom method (`-X`), repeatable headers (`-H`), inline body (`-d`) or body from file (`--data-file`), TLS skip verification (`-insecure`), and keep‑alive toggle (`-keepalive`).
- **Detailed summary** – total time, requests/sec, min/avg/max latency, and p50/p95/p99.
- **ASCII histogram** – log‑scale latency buckets with bar chart for quick distribution insight.
- **Request timeout** – configurable per‑request deadline.

## Installation

### Prerequisites
- Go 1.20 or later (any recent version works).

### Build from source
```bash
git clone https://github.com/MehdiShekari/go-http-load-test.git   
cd go-http-load-test
go build -o go-http-load-test main.go
```

Or run directly:
```bash
go run main.go -url http://example.com
```

## Usage

```bash
./go-http-load-test -url <target> [options]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-url` | string | *required* | Target URL. |
| `-n` | int | `200` | Total number of requests. |
| `-c` | int | `50` | Number of concurrent workers (goroutines). |
| `-X` | string | `GET` | HTTP method (GET, POST, PUT, etc.). |
| `-H` | header list | *none* | Header in `Name: Value` format. Repeatable. |
| `-d` | string | *none* | Request body (string). |
| `--data-file` | string | *none* | File containing request body. |
| `--timeout` | int | `30` | Request timeout in seconds. |
| `-keepalive` | bool | `true` | Use HTTP keep‑alive (persistent connections). |
| `-insecure` | bool | `false` | Skip TLS certificate verification. |
| `-version` | bool | `false` | Print version and exit. |

## Examples

### Basic GET load test
```bash
./go-http-load-test -url http://localhost:8080/api -n 500 -c 20
```

### POST with JSON body and custom headers
```bash
./go-http-load-test -url https://api.example.com/data \
  -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer token123" \
  -d '{"key":"value"}' \
  -n 200 -c 50
```

### POST with body from file
```bash
./go-http-load-test -url https://api.example.com/upload \
  -X POST --data-file payload.json \
  -n 100 -c 10
```

### High‑concurrency test with TLS skip
```bash
./go-http-load-test -url https://self-signed.local -insecure -n 2000 -c 200
```

## Sample Output

```
========== Load Test Summary ==========
URL:               http://localhost:8080/api
Requests:          200 total, 199 success, 1 errors
Concurrency:       20
Total time:        1.023s
Requests/sec:      194.52
Latency (min):     1.2ms
Latency (avg):     5.8ms
Latency (max):     18.3ms
Latency (p50):     5.6ms
Latency (p95):     10.1ms
Latency (p99):     14.7ms

Latency Distribution (log scale buckets):
[    0s -   1ms)     0 |
[   1ms -   2ms)    12 |███
[   2ms -   4ms)    45 |███████████
[   4ms -   8ms)   108 |█████████████████████████████
[   8ms -  16ms)    32 |████████
[  16ms -  32ms)     2 |
```

## Concurrency & Performance Design

- **Atomic slot allocation**: Each worker atomically grabs a unique index into a pre‑allocated latency slice. No locks, no dynamic allocation during the test.
- **Semaphore channel**: A buffered channel limits concurrent goroutines to the exact value of `-c`. Workers block when the channel is full, maintaining a steady worker pool.
- **Lock‑free error recording**: A parallel `uint32` slice uses atomic stores to mark failed requests.
- **Connection reuse**: HTTP keep‑alive is enabled by default; response bodies are drained and closed to allow connection recycling.

This architecture ensures minimal overhead and linear scaling on multi‑core machines.

## Limitations

- The tool does **not** follow redirects (by design, to isolate target latency).
- Only one URL / endpoint per run.
- No ramp‑up phase or dynamic rate control – it fires requests as fast as workers allow.
