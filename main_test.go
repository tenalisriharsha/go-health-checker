package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

// TestMain builds the CLI once so the tests below exercise the real binary,
// including its exit codes.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "go-health-checker-test")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "go-health-checker")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("build failed: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runCLI runs the binary in dir and returns its stdout, stderr and exit code.
func runCLI(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func writeConfig(t *testing.T, dir, name, url string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	cfg := `{"endpoints": [{"name": "svc", "url": "` + url + `"}]}`
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExitCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	healthy := writeConfig(t, dir, "healthy.json", srv.URL+"/")
	unhealthy := writeConfig(t, dir, "unhealthy.json", srv.URL+"/down")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"all healthy", []string{"-config", healthy}, 0},
		{"unhealthy endpoint", []string{"-config", unhealthy}, 1},
		{"missing config", []string{"-config", filepath.Join(dir, "nope.json")}, 2},
		{"unknown flag", []string{"-bogus"}, 2},
		{"help", []string{"-h"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, stderr, code := runCLI(t, dir, tc.args...); code != tc.want {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tc.want, stderr)
			}
		})
	}
}

// A positional argument used to be silently ignored, so
// `go-health-checker other.json` checked ./config.json instead.
func TestPositionalArgumentRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	dir := t.TempDir()
	writeConfig(t, dir, "config.json", srv.URL)
	other := writeConfig(t, dir, "other.json", "http://other.invalid")

	stdout, stderr, code := runCLI(t, dir, other)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("expected no report on stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "unexpected argument") {
		t.Errorf("stderr should explain the bad argument, got %q", stderr)
	}
}

// A negative -timeout used to be silently replaced by the config timeout.
func TestNegativeTimeoutRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := writeConfig(t, dir, "config.json", srv.URL)

	stdout, stderr, code := runCLI(t, dir, "-config", cfg, "-timeout", "-1s")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("expected no report on stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "-timeout must not be negative") {
		t.Errorf("stderr should explain the bad timeout, got %q", stderr)
	}
}
