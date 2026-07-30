// Package checker implements concurrent HTTP health checks and JSON reporting.
package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Result is the outcome of checking a single endpoint.
type Result struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMs  int64  `json:"latency_ms"`
	Healthy    bool   `json:"healthy"`
	Error      string `json:"error,omitempty"`
}

// Summary aggregates the results.
type Summary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
}

// Report is the full JSON document printed by the CLI.
type Report struct {
	CheckedAt string   `json:"checked_at"`
	Summary   Summary  `json:"summary"`
	Results   []Result `json:"results"`
}

// Checker performs HTTP health checks.
type Checker struct {
	Client  *http.Client
	Timeout time.Duration
}

// New returns a Checker with the given timeout.
func New(timeout time.Duration) *Checker {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Checker{
		Client:  &http.Client{Timeout: timeout},
		Timeout: timeout,
	}
}

// CheckAll checks every endpoint concurrently and returns a Report with
// results in the same order as the config endpoints.
func (c *Checker) CheckAll(endpoints []Endpoint) Report {
	results := make([]Result, len(endpoints))
	var wg sync.WaitGroup
	for i, ep := range endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.check(ep)
		}()
	}
	wg.Wait()

	report := Report{
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Results:   results,
	}
	report.Summary.Total = len(results)
	for _, r := range results {
		if r.Healthy {
			report.Summary.Healthy++
		} else {
			report.Summary.Unhealthy++
		}
	}
	return report
}

// check probes a single endpoint. Any 2xx or 3xx status counts as healthy.
func (c *Checker) check(ep Endpoint) Result {
	res := Result{Name: ep.Name, URL: ep.URL}

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.URL, nil)
	if err != nil {
		res.Error = fmt.Sprintf("build request: %v", err)
		return res
	}

	start := time.Now()
	resp, err := c.Client.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	// Drain a bounded amount so connections can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	res.StatusCode = resp.StatusCode
	res.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 400
	if !res.Healthy {
		res.Error = fmt.Sprintf("unexpected status %d", resp.StatusCode)
	}
	return res
}
