package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/updater"
)

// newBackupEngine builds the backup engine over the data dir and database.
// A site on object storage (MEDIA_STORAGE=s3) hands the engine its media
// store; with no database (list, delete) no store is built and no S3
// variable is read, so those commands never need the bucket.
func newBackupEngine(pool *pgxpool.Pool, mediaDir, version string, logger *slog.Logger) *backup.Engine {
	var store media.BlobStore
	if pool != nil {
		store = mediaStoreOrUnavailable(os.Getenv)
	}
	return backup.New(backup.Options{
		MediaStore: store,
		Data:       config.ResolveDataDir(), MediaDir: mediaDirOr(mediaDir), DatabaseURL: os.Getenv("DATABASE_URL"),
		Pool: pool, Run: backup.OSExec, Free: updater.FreeDisk, Log: logger, Version: version,
		SecretsDir: secretsDirFromEnv(), ConfigPath: os.Getenv("HATAMOTO_CONFIG_FILE"),
		Trust: trustIDs(logger),
	})
}

// mediaStoreOrUnavailable is the object storage store of the environment, nil
// for local media. Invalid settings give a store that fails every call with the
// reason, so a backup of such a site fails with it instead of treating the
// media as local.
func mediaStoreOrUnavailable(getenv func(string) string) media.BlobStore {
	store, err := media.NewStoreFromEnv(getenv, "/media")
	if err != nil {
		return media.UnavailableStore(err)
	}
	return store
}

// secretsDirFromEnv is where serve keeps secrets.env (see serve).
func secretsDirFromEnv() string {
	if dir := os.Getenv("HATAMOTO_SECRETS_DIR"); dir != "" {
		return dir
	}
	return "./data/secrets"
}

// trustIDs gives the key IDs a backup records or a restore compares: the
// release and manifest keys embedded in this binary, and the IDs of the keys
// in HATAMOTO_TRUSTED_KEYS (undecodable ones are skipped with a warning).
func trustIDs(logger *slog.Logger) func() (backup.TrustIDs, error) {
	return func() (backup.TrustIDs, error) {
		ids := backup.TrustIDs{Release: sortedIDs(release.ReleaseTrust()), Manifest: sortedIDs(release.ManifestTrust())}
		keys := config.LoadPackageConfig().TrustedKeys
		for _, f := range strings.FieldsFunc(keys, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
			pub, err := packages.ParsePublicKey(f)
			if err != nil {
				logger.Warn("backup: HATAMOTO_TRUSTED_KEYS holds a key that is not readable; it is not recorded")
				continue
			}
			ids.Env = append(ids.Env, packages.KeyID(pub))
		}
		sort.Strings(ids.Env)
		return ids, nil
	}
}

func sortedIDs(s packages.TrustSet) []string {
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// preUpdateBackups adapts the engine to updater.Backuper.
type preUpdateBackups struct{ eng *backup.Engine }

func (b preUpdateBackups) PreUpdate(ctx context.Context, target string) (string, error) {
	info, err := b.eng.Create(ctx, backup.Request{Kind: backup.KindPreUpdate, Target: target, Wait: 10 * time.Minute})
	if err != nil {
		return "", err
	}
	return "backup " + info.ID, nil
}

// noDatabaseBackups stands in when the CLI cannot reach a database: the
// update is refused rather than installed without a backup.
type noDatabaseBackups struct{}

func (noDatabaseBackups) PreUpdate(context.Context, string) (string, error) {
	return "", errors.New("database not reachable; use --no-backup")
}
