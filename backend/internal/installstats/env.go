package installstats

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Environment variables an operator can set.
const (
	// EnvTelemetry set to "off" forces install stats off. No value turns them
	// on: only the owner's choice does.
	EnvTelemetry = "HATAMOTO_TELEMETRY"
	// EnvURL overrides the endpoint, for testing.
	EnvURL = "HATAMOTO_TELEMETRY_URL"
)

// DefaultURL is the public endpoint install stats are sent to.
const DefaultURL = "https://hatamoto.dev/p/stats/v1/ping"

// ForcedOff reports HATAMOTO_TELEMETRY=off.
func ForcedOff(getenv func(string) string) bool {
	return strings.EqualFold(strings.TrimSpace(getenv(EnvTelemetry)), "off")
}

// ResolveURL returns the endpoint: HATAMOTO_TELEMETRY_URL when it is valid,
// else DefaultURL with a warning text saying why. The URL must be a plain
// https scheme://host/path; plain http to a loopback host is accepted only in
// test builds.
func ResolveURL(getenv func(string) string) (endpoint, warn string) {
	return resolveURL(getenv, allowLoopbackHTTP())
}

func resolveURL(getenv func(string) string, allowLoopback bool) (endpoint, warn string) {
	raw := strings.TrimSpace(getenv(EnvURL))
	if raw == "" {
		return DefaultURL, ""
	}
	if err := checkURL(raw, allowLoopback); err != nil {
		return DefaultURL, fmt.Sprintf("%s ignored: %v", EnvURL, err)
	}
	return raw, ""
}

func checkURL(raw string, allowLoopback bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a URL")
	}
	switch {
	case u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "?") || strings.Contains(raw, "#"):
		return fmt.Errorf("must be a plain scheme://host/path")
	case u.Scheme == "https":
	case u.Scheme == "http" && allowLoopback && isLoopbackHost(u.Hostname()):
	default:
		return fmt.Errorf("must be https")
	}
	return nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
