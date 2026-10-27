package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the connection settings. The API key only ever comes from the
// environment or an env file, never from a flag, so it cannot reach argv.
type Config struct {
	URL    string
	APIKey string
	// SchemaID is the id of the doc schema (optional, see Flags).
	SchemaID string
}

// String hides the key so a stray %v or log line cannot leak it.
func (c Config) String() string { return fmt.Sprintf("Config{URL: %s, APIKey: redacted}", c.URL) }

// GoString matches String for %#v.
func (c Config) GoString() string { return c.String() }

// defaultEnvFile is read when it exists and --env-file is not given.
const defaultEnvFile = ".docs-publish.env"

// baseDirEnv names the directory relative env file paths are resolved from.
// make sets it to the repository root because it runs the tool from its own
// module directory; without it paths are relative to the working directory.
const baseDirEnv = "DOCS_PUBLISH_BASE_DIR"

// LoadConfig reads HATAMOTO_URL, HATAMOTO_API_KEY and HATAMOTO_DOC_SCHEMA_ID
// from getenv, filling gaps from envFile. An empty envFile means the default
// file, which is skipped when absent; an explicit file must exist.
func LoadConfig(getenv func(string) string, envFile string) (Config, error) {
	explicit := envFile != ""
	if !explicit {
		envFile = defaultEnvFile
	}
	if base := strings.TrimSpace(getenv(baseDirEnv)); base != "" && !filepath.IsAbs(envFile) {
		envFile = filepath.Join(base, envFile)
	}
	file, err := readEnvFile(envFile)
	if err != nil && (explicit || !errors.Is(err, os.ErrNotExist)) {
		return Config{}, err
	}
	pick := func(k string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return file[k]
	}
	cfg := Config{
		URL:      strings.TrimRight(pick("HATAMOTO_URL"), "/"),
		APIKey:   pick("HATAMOTO_API_KEY"),
		SchemaID: pick("HATAMOTO_DOC_SCHEMA_ID"),
	}
	if cfg.URL == "" || cfg.APIKey == "" {
		return Config{}, fmt.Errorf("HATAMOTO_URL and HATAMOTO_API_KEY must be set (environment or env file %s)", envFile)
	}
	if err := checkURL(cfg.URL); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// checkURL accepts https, or http only for a loopback host.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("HATAMOTO_URL is not a valid URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" {
			return nil
		}
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return errors.New("HATAMOTO_URL must be https (http is allowed for loopback only)")
}

// readEnvFile parses KEY=VALUE lines; blank lines and # comments are skipped
// and one pair of surrounding quotes is removed. Values are never echoed.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, sc.Err()
}
