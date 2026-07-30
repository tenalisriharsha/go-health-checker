package checker

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseConfigValid(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"timeout_ms": 1500,
		"endpoints": [
			{"name": "a", "url": "http://localhost:8080/health"},
			{"name": "b", "url": "https://example.com"}
		]
	}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(cfg.Endpoints))
	}
	if got := cfg.Timeout(); got != 1500*time.Millisecond {
		t.Errorf("expected timeout 1.5s, got %v", got)
	}
}

func TestParseConfigDefaultTimeout(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"endpoints": [{"name": "a", "url": "http://x.test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.Timeout(); got != DefaultTimeout {
		t.Errorf("expected default timeout %v, got %v", DefaultTimeout, got)
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := map[string]string{
		"invalid json": `{not json`,
		"no endpoints": `{"endpoints": []}`,
		"missing name": `{"endpoints": [{"url": "http://x.test"}]}`,
		"missing url":  `{"endpoints": [{"name": "a"}]}`,
		"bad scheme":   `{"endpoints": [{"name": "a", "url": "ftp://x.test"}]}`,
		"relative url": `{"endpoints": [{"name": "a", "url": "/health"}]}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(input)); err == nil {
				t.Errorf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoadConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := `{"endpoints": [{"name": "a", "url": "http://x.test"}]}`
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoints[0].Name != "a" {
		t.Errorf("expected endpoint name %q, got %q", "a", cfg.Endpoints[0].Name)
	}
}

func TestCheckHealthyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := New(time.Second).check(Endpoint{Name: "ok", URL: srv.URL})
	if !res.Healthy {
		t.Errorf("expected healthy, got error %q", res.Error)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", res.StatusCode)
	}
}

func TestCheckRedirectCountsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := New(time.Second).check(Endpoint{Name: "redirect", URL: srv.URL})
	if !res.Healthy {
		t.Errorf("expected redirect chain to be healthy, got %q", res.Error)
	}
}

func TestCheckServerErrorUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := New(time.Second).check(Endpoint{Name: "bad", URL: srv.URL})
	if res.Healthy {
		t.Error("expected unhealthy for status 500")
	}
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", res.StatusCode)
	}
	if res.Error == "" {
		t.Error("expected error message for unhealthy endpoint")
	}
}

func TestCheckConnectionRefused(t *testing.T) {
	// Grab a free port and close it so nothing is listening.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	res := New(time.Second).check(Endpoint{Name: "down", URL: "http://" + addr})
	if res.Healthy {
		t.Error("expected unhealthy for refused connection")
	}
	if res.Error == "" {
		t.Error("expected error message for refused connection")
	}
}

func TestCheckTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	start := time.Now()
	res := New(50 * time.Millisecond).check(Endpoint{Name: "slow", URL: srv.URL})
	if res.Healthy {
		t.Error("expected unhealthy on timeout")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("check took %v, expected it to abort near the 50ms timeout", elapsed)
	}
}

func TestCheckAllPreservesOrderAndSummarizes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/bad", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	endpoints := []Endpoint{
		{Name: "first-ok", URL: srv.URL + "/ok"},
		{Name: "second-bad", URL: srv.URL + "/bad"},
		{Name: "third-ok", URL: srv.URL + "/ok"},
	}
	report := New(2 * time.Second).CheckAll(endpoints)

	if report.Summary.Total != 3 || report.Summary.Healthy != 2 || report.Summary.Unhealthy != 1 {
		t.Errorf("unexpected summary: %+v", report.Summary)
	}
	for i, ep := range endpoints {
		if report.Results[i].Name != ep.Name {
			t.Errorf("result %d: expected name %q, got %q", i, ep.Name, report.Results[i].Name)
		}
	}
	if report.CheckedAt == "" {
		t.Error("expected CheckedAt to be set")
	}
}

func TestCheckAllConcurrency(t *testing.T) {
	const delay = 100 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	endpoints := make([]Endpoint, 5)
	for i := range endpoints {
		endpoints[i] = Endpoint{Name: fmt.Sprintf("ep-%d", i), URL: srv.URL}
	}

	start := time.Now()
	report := New(2 * time.Second).CheckAll(endpoints)
	elapsed := time.Since(start)

	if report.Summary.Healthy != 5 {
		t.Errorf("expected 5 healthy, got %d", report.Summary.Healthy)
	}
	// Sequential would take >= 500ms; concurrent should finish well under.
	if elapsed > 400*time.Millisecond {
		t.Errorf("checks appear sequential: took %v for 5x%v", elapsed, delay)
	}
}
