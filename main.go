package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// headersList accumulates -H flags.
type headersList []string

func (h *headersList) String() string { return strings.Join(*h, ", ") }
func (h *headersList) Set(value string) error {
	*h = append(*h, value)
	return nil
}

func main() {
	// ---- Flags ----
	var (
		urlStr        string
		numReqs       int
		concurrency   int
		method        string
		headers       headersList
		bodyData      string
		dataFile      string
		timeoutSec    int
		keepAlive     bool
		insecure      bool
		showVersion   bool
	)
	flag.StringVar(&urlStr, "url", "", "Target URL (required)")
	flag.IntVar(&numReqs, "n", 200, "Total number of requests")
	flag.IntVar(&concurrency, "c", 50, "Number of concurrent workers")
	flag.StringVar(&method, "X", "GET", "HTTP method")
	flag.Var(&headers, "H", "Header \"Name: Value\" (repeatable)")
	flag.StringVar(&bodyData, "d", "", "Request body (string)")
	flag.StringVar(&dataFile, "data-file", "", "File containing request body")
	flag.IntVar(&timeoutSec, "timeout", 30, "Request timeout in seconds")
	flag.BoolVar(&keepAlive, "keepalive", true, "Use HTTP keep-alive")
	flag.BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("go-http-load-test v1.0")
		return
	}
	if urlStr == "" {
		flag.Usage()
		os.Exit(1)
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > numReqs {
		concurrency = numReqs
	}

	// ---- Build request config ----
	reqHeaders := make(http.Header)
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Invalid header: %q\n", h)
			os.Exit(1)
		}
		reqHeaders.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}

	var bodyReader io.Reader
	if dataFile != "" {
		data, err := os.ReadFile(dataFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading data file: %v\n", err)
			os.Exit(1)
		}
		bodyReader = strings.NewReader(string(data))
	} else if bodyData != "" {
		bodyReader = strings.NewReader(bodyData)
	}

	// ---- HTTP client ----
	transport := &http.Transport{
		DisableKeepAlives: !keepAlive,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeoutSec) * time.Second,
	}

	// ---- Shared state (lock-free) ----
	latencies := make([]time.Duration, numReqs) // pre-allocated
	errCounts := make([]uint32, numReqs)         // 1 if request failed
	var reqIndex uint64                         // atomic counter for slot allocation

	// ---- Worker function ----
	worker := func() {
		for {
			idx := atomic.AddUint64(&reqIndex, 1) - 1
			if int(idx) >= numReqs {
				return
			}
			start := time.Now()

			req, err := http.NewRequest(method, urlStr, bodyReader)
			if err != nil {
				atomic.StoreUint32(&errCounts[idx], 1)
				latencies[idx] = 0
				continue
			}
			req.Header = reqHeaders.Clone()

			resp, err := client.Do(req)
			lat := time.Since(start)
			if err != nil {
				atomic.StoreUint32(&errCounts[idx], 1)
				latencies[idx] = lat // still record the attempt time
			} else {
				// Drain and close body to reuse connection
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				latencies[idx] = lat
			}
		}
	}

	// ---- Launch workers ----
	startTime := time.Now()
	sem := make(chan struct{}, concurrency) // semaphore for concurrency
	for i := 0; i < concurrency; i++ {
		go func() {
			sem <- struct{}{}
			worker()
			<-sem
		}()
	}
	// Wait for all requests to be dispatched by checking the atomic index
	// We could use a WaitGroup, but spin‑wait is simpler with known total.
	// A small sleep loop avoids burning CPU.
	for atomic.LoadUint64(&reqIndex) < uint64(numReqs) {
		time.Sleep(1 * time.Millisecond)
	}
	// Wait for workers to finish (they exit when index >= numReqs)
	// The semaphore + the fact that workers exit allows a safe finish.
	// All workers will eventually drain because reqIndex has reached numReqs.
	// A tiny grace period ensures all in‑flight requests finish.
	time.Sleep(100 * time.Millisecond) // crude but effective
	totalTime := time.Since(startTime)

	// ---- Collect results ----
	var (
		successLats []time.Duration
		errCount    int
	)
	for i := 0; i < numReqs; i++ {
		if atomic.LoadUint32(&errCounts[i]) == 1 {
			errCount++
		} else {
			successLats = append(successLats, latencies[i])
		}
	}

	if len(successLats) == 0 {
		fmt.Println("All requests failed. No latency data to show.")
		return
	}

	// Sort for percentiles
	sort.Slice(successLats, func(i, j int) bool { return successLats[i] < successLats[j] })

	// ---- Statistics ----
	minLat := successLats[0]
	maxLat := successLats[len(successLats)-1]
	sumLat := time.Duration(0)
	for _, l := range successLats {
		sumLat += l
	}
	avgLat := sumLat / time.Duration(len(successLats))
	p50 := successLats[len(successLats)*50/100]
	p95 := successLats[len(successLats)*95/100]
	p99 := successLats[len(successLats)*99/100]
	rps := float64(len(successLats)) / totalTime.Seconds()

	fmt.Println("\n========== Load Test Summary ==========")
	fmt.Printf("URL:               %s\n", urlStr)
	fmt.Printf("Requests:          %d total, %d success, %d errors\n", numReqs, len(successLats), errCount)
	fmt.Printf("Concurrency:       %d\n", concurrency)
	fmt.Printf("Total time:        %v\n", totalTime.Round(time.Millisecond))
	fmt.Printf("Requests/sec:      %.2f\n", rps)
	fmt.Printf("Latency (min):     %v\n", minLat.Round(time.Microsecond))
	fmt.Printf("Latency (avg):     %v\n", avgLat.Round(time.Microsecond))
	fmt.Printf("Latency (max):     %v\n", maxLat.Round(time.Microsecond))
	fmt.Printf("Latency (p50):     %v\n", p50.Round(time.Microsecond))
	fmt.Printf("Latency (p95):     %v\n", p95.Round(time.Microsecond))
	fmt.Printf("Latency (p99):     %v\n", p99.Round(time.Microsecond))

	// ---- Histogram ----
	fmt.Println("\nLatency Distribution (log scale buckets):")
	buckets := generateBuckets(maxLat)
	counts := make([]int, len(buckets)-1)
	for _, lat := range successLats {
		// find bucket index
		idx := sort.Search(len(buckets)-1, func(i int) bool {
			return buckets[i+1] > lat
		})
		if idx < len(counts) {
			counts[idx]++
		}
	}
	// Find max count for bar scaling
	maxCount := 0
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}
	barWidth := 50
	for i := 0; i < len(buckets)-1; i++ {
		lower := buckets[i]
		upper := buckets[i+1]
		count := counts[i]
		barLen := 0
		if maxCount > 0 {
			barLen = count * barWidth / maxCount
		}
		bar := strings.Repeat("█", barLen)
		fmt.Printf("[%5v - %5v) %5d |%s\n",
			lower.Round(time.Microsecond),
			upper.Round(time.Microsecond),
			count,
			bar)
	}
}

// generateBuckets creates log-scale boundaries from 0 to max inclusive.
func generateBuckets(max time.Duration) []time.Duration {
	buckets := []time.Duration{0}
	if max == 0 {
		return []time.Duration{0, 1 * time.Millisecond}
	}
	// start with 1ms and double
	bound := 1 * time.Millisecond
	for bound <= max {
		buckets = append(buckets, bound)
		bound *= 2
	}
	// ensure we cover up to max
	if buckets[len(buckets)-1] < max {
		buckets = append(buckets, max)
	}
	return buckets
}