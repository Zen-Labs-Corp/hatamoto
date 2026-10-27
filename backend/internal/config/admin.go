package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/adminpath"
)

const (
	defaultNotFoundPerMin = 60
	defaultNotFoundBlock  = 2 * time.Minute
)

// AdminConfig controls the Go admin UI.
type AdminConfig struct {
	// SetupCode is HATAMOTO_SETUP_CODE: the code the first-run wizard asks
	// for, when you cannot read the logs (empty = generate one per boot).
	SetupCode string
	// TrustedProxies is HATAMOTO_TRUSTED_PROXIES: comma-separated CIDRs whose
	// X-Forwarded-* headers are believed (client IP, scheme, host). Empty means
	// loopback only.
	TrustedProxies []string
	// Dev is HATAMOTO_DEV=1: development-only admin pages (the component
	// styleguide at /admin/_styleguide). Never set it in production.
	Dev bool
	// Path is HATAMOTO_ADMIN_PATH: the admin address pinned by the operator
	// ("" = not pinned), lower-cased when it is well formed. It wins over the
	// stored address. A malformed value is kept as typed and fails Validate.
	Path string
	// Resolved is the address the server mounts the admin at. App boot sets it
	// after reading the environment and the database; empty means /admin.
	Resolved string
	// NotFoundPerMin is HATAMOTO_NOTFOUND_PER_MIN: how many not-found
	// responses one client IP may cause per minute before it is blocked from
	// the admin address (default 60, 0 disables the throttle).
	NotFoundPerMin int
	// NotFoundBlock is HATAMOTO_NOTFOUND_BLOCK: how long that block lasts
	// (default 2m, at least 1s).
	NotFoundBlock time.Duration

	// Parse errors, reported by Validate so Load stays infallible.
	pathErr, perMinErr, blockErr error
}

func loadAdminConfig() AdminConfig {
	var proxies []string
	for _, c := range strings.Split(os.Getenv("HATAMOTO_TRUSTED_PROXIES"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			if ip := net.ParseIP(c); ip != nil { // a bare address means just that host
				c += "/128"
				if ip.To4() != nil {
					c = ip.String() + "/32"
				}
			}
			proxies = append(proxies, c)
		}
	}
	a := AdminConfig{
		SetupCode:      strings.TrimSpace(os.Getenv("HATAMOTO_SETUP_CODE")),
		TrustedProxies: proxies,
		Dev:            os.Getenv("HATAMOTO_DEV") == "1",
		NotFoundPerMin: defaultNotFoundPerMin,
		NotFoundBlock:  defaultNotFoundBlock,
	}
	if raw := strings.TrimSpace(os.Getenv("HATAMOTO_ADMIN_PATH")); raw != "" {
		if p, err := adminpath.Normalize(raw); err != nil {
			a.Path = raw
			a.pathErr = fmt.Errorf("HATAMOTO_ADMIN_PATH: %q is not a valid admin address (one segment of 4 to 64 lowercase letters, digits or dashes, like /my-place)", raw)
		} else {
			a.Path = p
		}
	}
	if raw := strings.TrimSpace(os.Getenv("HATAMOTO_NOTFOUND_PER_MIN")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			a.perMinErr = fmt.Errorf("HATAMOTO_NOTFOUND_PER_MIN: %q is not a whole number of 0 or more (0 turns the throttle off)", raw)
		} else {
			a.NotFoundPerMin = n
		}
	}
	if raw := strings.TrimSpace(os.Getenv("HATAMOTO_NOTFOUND_BLOCK")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Second {
			a.blockErr = fmt.Errorf("HATAMOTO_NOTFOUND_BLOCK: %q is not a duration of 1s or more like 2m (to turn the throttle off set HATAMOTO_NOTFOUND_PER_MIN=0)", raw)
		} else {
			a.NotFoundBlock = d
		}
	}
	return a
}

const minSetupCodeLen = 12

// NormalizeSetupCode is the form a setup code is compared in: upper case, with
// spaces and dashes removed. The length and repetition rules apply to it, so
// padding that the comparison ignores cannot make a weak code pass.
func NormalizeSetupCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, strings.ToUpper(s))
}

// trivialCode reports a code made of fewer than four distinct characters, or
// of one short unit repeated ("aaaaaaaaaaaa", "abcabcabcabc").
func trivialCode(code string) bool {
	distinct := map[rune]struct{}{}
	for _, r := range code {
		distinct[r] = struct{}{}
	}
	if len(distinct) < 4 {
		return true
	}
	for unit := 1; unit <= len(code)/2; unit++ {
		if len(code)%unit == 0 && strings.Repeat(code[:unit], len(code)/unit) == code {
			return true
		}
	}
	return false
}

// CheckSetupCode reports why HATAMOTO_SETUP_CODE is too weak to use, or nil
// (also when it is unset). It is not part of Validate: only an install that
// is still waiting for its first owner needs the rule, and that is known only
// once the database is open (the server enforces it then; an upgraded install
// that already has users just logs a warning).
func (a AdminConfig) CheckSetupCode() error {
	if a.SetupCode == "" {
		return nil
	}
	code := NormalizeSetupCode(a.SetupCode)
	if len([]rune(code)) < minSetupCodeLen {
		return fmt.Errorf("HATAMOTO_SETUP_CODE must be at least %d characters (case, spaces and dashes do not count)", minSetupCodeLen)
	}
	if trivialCode(code) {
		return fmt.Errorf("HATAMOTO_SETUP_CODE is too guessable: use a code that is not one repeated character or pattern")
	}
	return nil
}

// Validate rejects an unusable trusted-proxy list, a malformed
// HATAMOTO_ADMIN_PATH and bad HATAMOTO_NOTFOUND_* values.
func (a AdminConfig) Validate() error {
	for _, err := range []error{a.pathErr, a.perMinErr, a.blockErr} {
		if err != nil {
			return err
		}
	}
	for _, c := range a.TrustedProxies {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return fmt.Errorf("HATAMOTO_TRUSTED_PROXIES: %q is not a CIDR like 10.0.0.0/8", c)
		}
	}
	return nil
}
