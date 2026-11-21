package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// PurgeConfig is the optional cache purge webhook (HATAMOTO_PURGE_*).
type PurgeConfig struct {
	URL          string // HATAMOTO_PURGE_URL; "" when off
	Token        string // HATAMOTO_PURGE_TOKEN; sent as Authorization: Bearer
	AllowPrivate bool   // HATAMOTO_PURGE_ALLOW_PRIVATE: the URL may be on a private or loopback address

	// Parse error, reported by Validate so Load stays infallible. It never
	// includes the token.
	urlErr error
}

func loadPurgeConfig() PurgeConfig {
	var p PurgeConfig
	raw := strings.TrimSpace(os.Getenv("HATAMOTO_PURGE_URL"))
	if raw != "" && !strings.EqualFold(raw, "off") {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") ||
			u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			p.urlErr = fmt.Errorf("HATAMOTO_PURGE_URL is not an absolute http or https URL without credentials (or off)")
		} else {
			p.URL = raw
		}
	}
	p.Token = strings.TrimSpace(os.Getenv("HATAMOTO_PURGE_TOKEN"))
	if strings.EqualFold(raw, "off") {
		p.Token = ""
	}
	if p.Token != "" && p.URL == "" && p.urlErr == nil {
		p.urlErr = fmt.Errorf("HATAMOTO_PURGE_TOKEN is set but HATAMOTO_PURGE_URL is not")
	}
	if v := strings.TrimSpace(os.Getenv("HATAMOTO_PURGE_ALLOW_PRIVATE")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			if p.urlErr == nil {
				p.urlErr = fmt.Errorf("HATAMOTO_PURGE_ALLOW_PRIVATE: %q is not a boolean", v)
			}
		} else {
			p.AllowPrivate = b
		}
	}
	return p
}

// Validate rejects a malformed HATAMOTO_PURGE_* setting.
func (p PurgeConfig) Validate() error { return p.urlErr }
