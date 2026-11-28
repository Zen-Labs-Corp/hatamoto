// Package runenv is the process run mode set by HATAMOTO_ENV: production
// (default) or staging. In staging every outbound sender stops at its gate.
package runenv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Mode is the process run mode.
type Mode string

const (
	ModeProduction Mode = "production"
	ModeStaging    Mode = "staging"

	// EnvVar is the environment variable that selects the mode.
	EnvVar = "HATAMOTO_ENV"

	// Banner is the text shown on every admin page in staging.
	Banner = "Staging: nothing is sent from this site."
)

// Parse reads a HATAMOTO_ENV value. Empty means production.
func Parse(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "production":
		return ModeProduction, nil
	case "staging":
		return ModeStaging, nil
	}
	return "", fmt.Errorf("invalid %s %q: use production or staging (or leave it unset)", EnvVar, raw)
}

// Staging reports whether the process runs in staging. It reads the
// environment on every call (like EMAIL_DISABLED). A value Parse rejects
// counts as staging (fail safe; config.Load refuses it at boot anyway).
func Staging() bool {
	m, err := Parse(os.Getenv(EnvVar))
	return err != nil || m == ModeStaging
}

// Family is one kind of outbound sender that staging switches off.
type Family string

const (
	Mail             Family = "mail"
	Webhooks         Family = "webhooks"
	IndexNow         Family = "IndexNow"
	Purge            Family = "cache purge"
	OffsiteBackup    Family = "off-site backup"
	Updater          Family = "updater"
	PluginHTTP       Family = "plugin outbound HTTP"
	InstallStats     Family = "install statistics"
	StaticExportS3   Family = "static export to S3"
	StaticExportSFTP Family = "static export over SFTP"
	MediaDelete      Family = "media deletions in object storage"
)

// Families lists every sender family in boot-line order.
func Families() []Family {
	return []Family{Mail, Webhooks, IndexNow, Purge, OffsiteBackup, Updater, PluginHTTP, InstallStats, StaticExportS3, StaticExportSFTP, MediaDelete}
}

// ErrSuppressed is returned by a sender that staging mode stopped.
var ErrSuppressed = errors.New("staging mode: nothing is sent from this site")

// Suppress reports whether f must not send. In staging it logs one line per
// call. A nil log uses slog.Default().
func Suppress(ctx context.Context, log *slog.Logger, f Family, attrs ...any) bool {
	if !Staging() {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	log.InfoContext(ctx, "staging mode: suppressed", append([]any{"sender", string(f)}, attrs...)...)
	return true
}

// Summary is the boot line logged by serve in staging.
func Summary() string {
	fs := Families()
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = string(f)
	}
	last := len(names) - 1
	return "staging mode: " + strings.Join(names[:last], ", ") + " and " + names[last] + " are off"
}
