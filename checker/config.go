package checker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"
)

// DefaultTimeout is used when the config file does not specify one.
const DefaultTimeout = 5 * time.Second

// Endpoint is a single HTTP endpoint to check.
type Endpoint struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Config is the top-level structure of the JSON config file.
type Config struct {
	TimeoutMs int        `json:"timeout_ms"`
	Endpoints []Endpoint `json:"endpoints"`
}

// Timeout returns the configured timeout, or DefaultTimeout if unset/invalid.
func (c Config) Timeout() time.Duration {
	if c.TimeoutMs <= 0 {
		return DefaultTimeout
	}
	return time.Duration(c.TimeoutMs) * time.Millisecond
}

// LoadConfig reads and validates a JSON config file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return ParseConfig(data)
}

// configKeys and endpointKeys list the exact (case-sensitive) JSON keys
// documented for Config and Endpoint. encoding/json's DisallowUnknownFields
// matches field tags case-insensitively before rejecting unknown fields, so
// a key that differs only in case (e.g. "Timeout_Ms") would otherwise be
// silently accepted instead of being treated as a typo.
var configKeys = map[string]bool{"timeout_ms": true, "endpoints": true}
var endpointKeys = map[string]bool{"name": true, "url": true}

func checkExactKeys(data []byte, allowed map[string]bool, context string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("%s: unknown field %q", context, key)
		}
	}
	return nil
}

// ParseConfig parses and validates config JSON.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	var rawCfg struct {
		Endpoints []json.RawMessage `json:"endpoints"`
	}
	if err := json.Unmarshal(data, &rawCfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := checkExactKeys(data, configKeys, "config"); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	for i, rawEp := range rawCfg.Endpoints {
		if err := checkExactKeys(rawEp, endpointKeys, fmt.Sprintf("endpoint %d", i)); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}

	if len(cfg.Endpoints) == 0 {
		return Config{}, fmt.Errorf("config must define at least one endpoint")
	}
	for i, ep := range cfg.Endpoints {
		if ep.Name == "" {
			return Config{}, fmt.Errorf("endpoint %d: name is required", i)
		}
		u, err := url.Parse(ep.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("endpoint %q: invalid url %q", ep.Name, ep.URL)
		}
	}
	return cfg, nil
}
