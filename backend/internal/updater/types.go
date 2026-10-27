// Package updater is the on-box update agent: it reads the signed channel
// manifest, picks a target under policy, and stages it for the supervisor.
package updater

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/core/internal/release"
)

// Update modes.
const (
	ModeAuto   = "auto"
	ModeNotify = "notify"
	ModeOff    = "off"

	// DefaultChannel is the channel a box follows when nothing says otherwise.
	DefaultChannel = "stable"

	// MaxDeferDays is the longest a non-security release may be held back.
	MaxDeferDays = 14
)

// Environment variables that override the settings row.
const (
	EnvMode    = "HATAMOTO_UPDATES"
	EnvChannel = "HATAMOTO_UPDATE_CHANNEL"
)

var pinPattern = regexp.MustCompile(`^v\d+\.\d+$`)

// Settings is the update policy. Mode and Channel are empty when unset;
// Effective fills the defaults. A nil window hour means "any time".
type Settings struct {
	Mode        string
	Channel     string
	WindowStart *int
	WindowEnd   *int
	Pin         string // "vX.Y" or ""
	DeferDays   int    // 0..MaxDeferDays
}

// Status is the outcome of the last run. Decision is one of up_to_date,
// scheduled, waiting_window, waiting_defer, blocked, available_major, staged,
// applying, rolled_back_yanked, off, notify, busy, dev; LastResult is ok,
// error or skipped; Reason is human text.
type Status struct {
	LastCheckAt    *time.Time
	LastResult     string
	Decision       string
	Reason         string
	Target         string
	AvailableMajor string
	FirstSeen      map[string]time.Time
}

// Validate checks the policy values and clamps DeferDays into range.
func (s *Settings) Validate() error {
	switch s.Mode {
	case "", ModeAuto, ModeNotify, ModeOff:
	default:
		return fmt.Errorf("update mode %q is not auto, notify or off", s.Mode)
	}
	if s.Channel != "" && !slices.Contains(release.Channels, s.Channel) {
		return fmt.Errorf("update channel %q is not one of %v", s.Channel, release.Channels)
	}
	for _, h := range []*int{s.WindowStart, s.WindowEnd} {
		if h != nil && (*h < 0 || *h > 23) {
			return fmt.Errorf("window hour %d is outside 0-23", *h)
		}
	}
	if s.Pin != "" && !pinPattern.MatchString(s.Pin) {
		return fmt.Errorf("pin %q is not of the form vMAJOR.MINOR", s.Pin)
	}
	s.DeferDays = min(max(s.DeferDays, 0), MaxDeferDays)
	return nil
}

// ParseMode reads a HATAMOTO_UPDATES value: off|false|0|no|disabled,
// on|auto|true|1, or notify, in any case.
func ParseMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "false", "0", "no", "disabled":
		return ModeOff, true
	case "on", "auto", "true", "1":
		return ModeAuto, true
	case "notify":
		return ModeNotify, true
	}
	return "", false
}

// Effective returns s with the defaults filled and the environment applied.
// Environment values win over the row. An unrecognised HATAMOTO_UPDATES fails
// closed to off (with a warning), since a mistyped "false" must not leave the
// box calling out and installing; an invalid channel is logged and ignored.
func (s Settings) Effective(env func(string) string) Settings {
	if s.Mode == "" {
		s.Mode = ModeAuto
	}
	if s.Channel == "" {
		s.Channel = DefaultChannel
	}
	if v := env(EnvMode); strings.TrimSpace(v) != "" {
		if m, ok := ParseMode(v); ok {
			s.Mode = m
		} else {
			s.Mode = ModeOff
			slog.Warn("unrecognised update mode in environment, treating it as off; use auto, notify or off",
				"var", EnvMode, "value", v)
		}
	}
	if v := env(EnvChannel); v != "" {
		if slices.Contains(release.Channels, v) {
			s.Channel = v
		} else {
			slog.Warn("ignoring invalid update channel in environment", "var", EnvChannel, "value", v)
		}
	}
	s.DeferDays = min(max(s.DeferDays, 0), MaxDeferDays)
	return s
}
