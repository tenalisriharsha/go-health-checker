// Command go-health-checker probes HTTP endpoints from a JSON config file and
// prints a JSON report of status codes and latency. It exits non-zero if any
// endpoint is unhealthy.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"go-health-checker/checker"
)

func main() {
	configPath := flag.String("config", "config.json", "path to JSON config file")
	timeout := flag.Duration("timeout", 0, "per-request timeout (overrides config, e.g. 2s)")
	flag.Parse()

	cfg, err := checker.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	effectiveTimeout := cfg.Timeout()
	if *timeout > 0 {
		effectiveTimeout = *timeout
	}

	report := checker.New(effectiveTimeout).CheckAll(cfg.Endpoints)

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode report: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if report.Summary.Unhealthy > 0 {
		os.Exit(1)
	}
}
