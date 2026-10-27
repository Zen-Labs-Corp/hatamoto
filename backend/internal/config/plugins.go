package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PluginConfig controls the WASM plugin runtime.
type PluginConfig struct {
	// Dir is HATAMOTO_PLUGIN_DIR, a development override: one <name>/plugin.json
	// + plugin.wasm per plugin, loaded after the installed packages and
	// replacing a package of the same name. Empty (the default) loads none.
	Dir string
	// CacheDir is where compiled wasm is cached across restarts:
	// HATAMOTO_PLUGIN_CACHE_DIR, else <HATAMOTO_DATA>/cache/wasm. The cache
	// is rebuildable and created private (0700). "off" keeps it in memory.
	CacheDir string
	// AllowPublicDBObjects is HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1: grant
	// db.* although objects the application role does not own are open to
	// PUBLIC (otherwise db.* is refused until their owner closes them).
	AllowPublicDBObjects bool
	// FetchPerMinute is HATAMOTO_PLUGIN_FETCH_PER_MINUTE: the most outbound
	// http.fetch requests one plugin may make per minute (0: the built-in
	// default of 600).
	FetchPerMinute int
}

// DefaultDataDir is HATAMOTO_DATA when unset: the server's writable state
// directory (the Docker image creates /app/data).
const DefaultDataDir = "./data"

// ResolveDataDir is HATAMOTO_DATA (or DefaultDataDir) made absolute against
// the working directory, so a relative value cannot drift once a process
// hands it to a child.
func ResolveDataDir() string {
	d := strings.TrimSpace(os.Getenv("HATAMOTO_DATA"))
	if d == "" {
		d = DefaultDataDir
	}
	if a, err := filepath.Abs(d); err == nil {
		return a
	}
	return d
}

// WasmCacheSubdir is the compile cache's place under the data directory.
var WasmCacheSubdir = filepath.Join("cache", "wasm")

// LoadPluginConfig reads the plugin env vars.
func LoadPluginConfig() PluginConfig {
	c := PluginConfig{
		Dir:      strings.TrimSpace(os.Getenv("HATAMOTO_PLUGIN_DIR")),
		CacheDir: strings.TrimSpace(os.Getenv("HATAMOTO_PLUGIN_CACHE_DIR")),

		AllowPublicDBObjects: strings.TrimSpace(os.Getenv("HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS")) == "1",
	}
	switch c.CacheDir {
	case "":
		data := strings.TrimSpace(os.Getenv("HATAMOTO_DATA"))
		if data == "" {
			data = DefaultDataDir
		}
		c.CacheDir = filepath.Join(data, WasmCacheSubdir)
	case "off":
		c.CacheDir = ""
	}
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("HATAMOTO_PLUGIN_FETCH_PER_MINUTE"))); err == nil && n > 0 {
		c.FetchPerMinute = n
	}
	return c
}
