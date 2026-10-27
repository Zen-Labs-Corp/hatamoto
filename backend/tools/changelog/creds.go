package main

import (
	"bufio"
	"fmt"
	"strings"
)

// Creds are the site connection settings. They come from the environment or a
// gitignored env file, never from argv.
type Creds struct{ URL, Key, SchemaID string }

const (
	envURL    = "HATAMOTO_URL"
	envKey    = "HATAMOTO_API_KEY"
	envSchema = "HATAMOTO_CHANGELOG_SCHEMA_ID"
)

// loadCreds reads each setting from the environment first, then from the first
// existing file in paths (KEY=VALUE lines). The error names the missing
// settings only, never a value.
func loadCreds(getenv func(string) string, readFile func(string) ([]byte, error), paths []string) (Creds, error) {
	file := map[string]string{}
	for _, p := range paths {
		data, err := readFile(p)
		if err != nil {
			continue
		}
		file = parseEnvFile(string(data))
		break
	}
	get := func(name string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return file[name]
	}
	c := Creds{URL: get(envURL), Key: get(envKey), SchemaID: get(envSchema)}
	var missing []string
	for _, m := range []struct{ name, val string }{{envURL, c.URL}, {envKey, c.Key}, {envSchema, c.SchemaID}} {
		if m.val == "" {
			missing = append(missing, m.name)
		}
	}
	if len(missing) > 0 {
		return Creds{}, fmt.Errorf("missing credentials: %s (set them in the environment or .hatamoto-changelog.env)", strings.Join(missing, ", "))
	}
	return c, nil
}

// parseEnvFile reads KEY=VALUE lines; blank lines and # comments are skipped,
// an optional "export " prefix and one pair of quotes around the value are
// removed.
func parseEnvFile(s string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}
