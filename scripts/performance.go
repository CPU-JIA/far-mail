// FAR Mail read-only HTTP performance smoke test.
//
// Examples:
//
//	go run ./scripts/performance.go
//	FAR_MAIL_API_TOKEN=... go run ./scripts/performance.go -base-url https://mail.example.com
//	go run ./scripts/performance.go -requests 1000 -concurrency 50 -output perf.txt
//
// The tool never prints credentials. API and console credentials are read from
// FAR_MAIL_API_TOKEN and FAR_MAIL_ADMIN_KEY when the authenticated scenarios
// are enabled.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type scenario struct {
	name   string
	path   string
	header string
	value  string
}

type sample struct {
	duration time.Duration
	err      error
	status   int
}

type result struct {
	name       string
	requests   int
	success    int
	errors     int
	wall       time.Duration
	average    time.Duration
	p50        time.Duration
	p95        time.Duration
	p99        time.Duration
	statusCode int
}

func percentile(values []time.Duration, fraction float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values))*fraction + 0.999999)
	if index < 1 {
		index = 1
	}
	if index > len(values) {
		index = len(values)
	}
	return values[index-1]
}

func runScenario(client *http.Client, baseURL string, item scenario, requests, concurrency int) result {
	started := time.Now()
	samples := make(chan sample, requests)
	jobs := make(chan struct{}, concurrency)
	var workers sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				requestStarted := time.Now()
				request, err := http.NewRequest(http.MethodGet, baseURL+item.path, nil)
				if err != nil {
					samples <- sample{duration: time.Since(requestStarted), err: err}
					continue
				}
				if item.header != "" {
					request.Header.Set(item.header, item.value)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				request = request.WithContext(ctx)
				response, requestErr := client.Do(request)
				if requestErr == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				cancel()
				samples <- sample{duration: time.Since(requestStarted), err: requestErr, status: statusOrZero(response)}
			}
		}()
	}

	for i := 0; i < requests; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	workers.Wait()
	close(samples)

	values := make([]time.Duration, 0, requests)
	output := result{name: item.name, requests: requests, wall: time.Since(started)}
	for current := range samples {
		if current.err == nil && current.status >= http.StatusOK && current.status < http.StatusMultipleChoices {
			output.success++
			if output.statusCode == 0 {
				output.statusCode = current.status
			}
		} else {
			output.errors++
		}
		values = append(values, current.duration)
	}
	if len(values) == 0 {
		return output
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var total time.Duration
	for _, value := range values {
		total += value
	}
	output.average = total / time.Duration(len(values))
	output.p50 = percentile(values, 0.50)
	output.p95 = percentile(values, 0.95)
	output.p99 = percentile(values, 0.99)
	return output
}

func statusOrZero(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func formatResult(value result) string {
	rps := 0.0
	if value.wall > 0 {
		rps = float64(value.success) / value.wall.Seconds()
	}
	return fmt.Sprintf(
		"%-22s requests=%-5d success=%-5d errors=%-5d status=%-3d wall=%8.2fms rps=%8.2f avg=%8.2fms p50=%8.2fms p95=%8.2fms p99=%8.2fms",
		value.name,
		value.requests,
		value.success,
		value.errors,
		value.statusCode,
		float64(value.wall)/float64(time.Millisecond),
		rps,
		float64(value.average)/float64(time.Millisecond),
		float64(value.p50)/float64(time.Millisecond),
		float64(value.p95)/float64(time.Millisecond),
		float64(value.p99)/float64(time.Millisecond),
	)
}

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:18081", "FAR Mail HTTP origin without a trailing slash")
	requests := flag.Int("requests", 200, "requests per scenario")
	concurrency := flag.Int("concurrency", 20, "maximum concurrent requests")
	warmup := flag.Int("warmup", 5, "warm-up requests per scenario")
	outputPath := flag.String("output", "", "optional path for the text report")
	flag.Parse()

	if *requests < 1 || *concurrency < 1 || *concurrency > *requests || *warmup < 0 {
		fmt.Fprintln(os.Stderr, "requests must be positive, concurrency must be between 1 and requests, and warmup must not be negative")
		os.Exit(2)
	}
	base := strings.TrimRight(strings.TrimSpace(*baseURL), "/")
	if base == "" {
		fmt.Fprintln(os.Stderr, "base-url must not be empty")
		os.Exit(2)
	}

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency * 2,
		MaxConnsPerHost:     *concurrency * 2,
		ForceAttemptHTTP2:   true,
	}
	client := &http.Client{Transport: transport}
	defer transport.CloseIdleConnections()

	items := []scenario{
		{name: "health", path: "/health"},
		{name: "public_settings", path: "/public/v1/settings"},
	}
	if token := strings.TrimSpace(os.Getenv("FAR_MAIL_API_TOKEN")); token != "" {
		items = append(items, scenario{name: "api_domains", path: "/api/v1/domains", header: "Authorization", value: "Bearer " + token})
	}
	if key := strings.TrimSpace(os.Getenv("FAR_MAIL_ADMIN_KEY")); key != "" {
		items = append(items, scenario{name: "console_summary", path: "/console/v1/system/summary", header: "X-Admin-Key", value: key})
	}

	lines := []string{
		"FAR Mail read-only HTTP performance test",
		"base_url=" + base,
		fmt.Sprintf("requests=%d concurrency=%d warmup=%d", *requests, *concurrency, *warmup),
		"credentials=loaded from environment; values are never printed",
	}
	for _, item := range items {
		for i := 0; i < *warmup; i++ {
			request, err := http.NewRequest(http.MethodGet, base+item.path, nil)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if item.header != "" {
				request.Header.Set(item.header, item.value)
			}
			response, err := client.Do(request)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warmup failed for %s: %v\n", item.name, err)
				os.Exit(1)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		lines = append(lines, formatResult(runScenario(client, base, item, *requests, *concurrency)))
	}

	report := strings.Join(lines, "\n") + "\n"
	fmt.Print(report)
	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, []byte(report), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write report: %v\n", err)
			os.Exit(1)
		}
	}
}
