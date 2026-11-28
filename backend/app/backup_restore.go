package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/backupcrypt"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/supervisor"
)

const backupRestoreUsage = "usage: hatamoto backup restore (<id|path.tar> | --from-offsite <id>) --admin-url <DSN> [--dry-run] [--config-out <path>] [--force] [--identity <file> | --passphrase-stdin]" +
	" [--offsite-endpoint U] [--offsite-region R] [--offsite-bucket B] [--offsite-prefix P] [--offsite-access-key K]"

// offsiteFlags are the flags that name an off-site target on the command line.
// Each overrides its HATAMOTO_OFFSITE_* variable. The secret key has no flag so
// it never shows in a process list or shell history: it comes from the
// environment only.
type offsiteFlags struct{ endpoint, region, bucket, prefix, accessKey string }

func (o *offsiteFlags) define(fs *flag.FlagSet) {
	fs.StringVar(&o.endpoint, "offsite-endpoint", "", "off-site endpoint URL (default "+offsite.EnvEndpoint+")")
	fs.StringVar(&o.region, "offsite-region", "", "off-site region (default "+offsite.EnvRegion+")")
	fs.StringVar(&o.bucket, "offsite-bucket", "", "off-site bucket (default "+offsite.EnvBucket+")")
	fs.StringVar(&o.prefix, "offsite-prefix", "", "off-site key prefix (default "+offsite.EnvPrefix+")")
	fs.StringVar(&o.accessKey, "offsite-access-key", "", "off-site access key (default "+offsite.EnvAccessKey+")")
}

// target reads the target from the flags, then the environment. It never reads
// the database: a restore runs on a machine that has none.
func (o offsiteFlags) target() (offsite.Target, error) {
	over := map[string]string{
		offsite.EnvEndpoint: o.endpoint, offsite.EnvRegion: o.region, offsite.EnvBucket: o.bucket,
		offsite.EnvPrefix: o.prefix, offsite.EnvAccessKey: o.accessKey,
	}
	return offsite.TargetFromEnv(func(k string) string {
		if v := over[k]; v != "" {
			return v
		}
		return os.Getenv(k)
	})
}

// restoreArgs are the parsed flags and source of `backup restore`.
type restoreArgs struct {
	source, adminURL, configOut string
	fromOffsite                 string
	dryRun, force               bool
	offsite                     offsiteFlags
	identity                    string // age private key file of an encrypted backup
	passphraseStdin             bool   // read the passphrase of an encrypted backup from stdin
}

func parseRestoreArgs(args []string) (restoreArgs, error) {
	var a restoreArgs
	pos, err := flagSet("backup restore", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&a.dryRun, "dry-run", false, "run every check, write nothing")
		fs.BoolVar(&a.force, "force", false, "restore a backup made with a different server secret and drop its unreadable commenter emails (2FA, SMTP password, off-site key and spam secret stay unreadable too; see docs/BACKUPS.md)")
		fs.StringVar(&a.adminURL, "admin-url", "", "superuser DSN for the role bootstrap")
		fs.StringVar(&a.configOut, "config-out", "", "absolute path to write the saved config to; without it the config is not written")
		fs.StringVar(&a.fromOffsite, "from-offsite", "", "restore backup `id` from the off-site bucket instead of a local archive")
		fs.StringVar(&a.identity, "identity", "", "age private key `file` for an encrypted backup (.tar.age)")
		fs.BoolVar(&a.passphraseStdin, "passphrase-stdin", false, "read the passphrase of an encrypted backup from stdin")
		a.offsite.define(fs)
	})
	if err == nil && a.identity != "" && a.passphraseStdin {
		return a, errors.New("use either --identity or --passphrase-stdin")
	}
	want := 1 // exactly one of the positional source and --from-offsite
	if a.fromOffsite != "" {
		want = 0
	}
	if err != nil || len(pos) != want || a.adminURL == "" {
		return a, errors.New(backupRestoreUsage)
	}
	if want == 1 {
		a.source = pos[0]
	}
	return a, nil
}

// runBackupRestore restores a backup onto a clean machine. It does not open
// the database through openDB (that is for a migrated one): the engine checks
// the empty target itself and opens its own pool after the core restore.
func runBackupRestore(ctx context.Context, args []string, out io.Writer, logger *slog.Logger) error {
	a, err := parseRestoreArgs(args)
	if err != nil {
		return err
	}
	if err := checkOwner(supervisor.NewLayout(config.ResolveDataDir())); err != nil {
		return err
	}
	// Resolved before the download, so a bad key file fails before a long transfer.
	identity, err := restoreIdentity(a, os.Stdin, os.Stderr)
	if err != nil {
		return err
	}
	if a.fromOffsite != "" {
		path, cleanup, err := fetchOffsite(ctx, a)
		if err != nil {
			return err
		}
		defer cleanup()
		a.source = path
	}
	ver, _ := buildInfo()
	mediaStore, err := media.NewStoreFromEnv(os.Getenv, "/media")
	if err != nil {
		return err
	}
	plan, err := backup.Restore(ctx, backup.RestoreOptions{
		Data:        config.ResolveDataDir(),
		MediaDir:    mediaDirOr(os.Getenv("MEDIA_UPLOAD_DIR")),
		MediaStore:  mediaStore,
		SecretsDir:  secretsDirFromEnv(),
		DatabaseURL: directDatabaseURL(),
		Version:     ver,
		Run:         backup.OSExec,
		Trust:       trustIDs(logger),
		Log:         logger,
	}, backup.RestoreRequest{Source: a.source, AdminURL: a.adminURL, ConfigOut: a.configOut, DryRun: a.dryRun, Force: a.force, Identity: identity})
	if errors.Is(err, backupcrypt.ErrWrongKey) {
		return errors.New("the private key or passphrase does not open this backup; nothing was restored")
	}
	if err != nil {
		return err
	}
	plan.Print(out)
	return nil
}

// maxSecretFile caps what is read as a key file or passphrase.
const maxSecretFile = 1 << 20

// restoreIdentity returns what opens an encrypted backup, or nil-safe lazily
// asks for it. A key file or stdin passphrase is read now; without either flag
// the prompt runs only if the backup turns out to be encrypted. No error text
// carries a key or passphrase.
func restoreIdentity(a restoreArgs, stdin *os.File, stderr io.Writer) (func() (*backupcrypt.Identity, error), error) {
	fixed := func(id *backupcrypt.Identity) func() (*backupcrypt.Identity, error) {
		return func() (*backupcrypt.Identity, error) { return id, nil }
	}
	switch {
	case a.identity != "":
		f, err := os.Open(a.identity)
		if err != nil {
			return nil, fmt.Errorf("cannot read the identity file: %w", err)
		}
		defer f.Close()
		id, err := backupcrypt.ParseIdentityFile(io.LimitReader(f, maxSecretFile))
		if err != nil {
			return nil, err
		}
		return fixed(id), nil
	case a.passphraseStdin:
		b, err := io.ReadAll(io.LimitReader(stdin, maxSecretFile))
		if err != nil {
			return nil, errors.New("cannot read the passphrase from stdin")
		}
		id, err := backupcrypt.PassphraseIdentity(trimOneNewline(string(b)))
		if err != nil {
			return nil, err
		}
		return fixed(id), nil
	}
	return func() (*backupcrypt.Identity, error) {
		fd := int(stdin.Fd())
		if !term.IsTerminal(fd) {
			return nil, errors.New("the backup is encrypted: pass --identity <file>, or --passphrase-stdin")
		}
		fmt.Fprint(stderr, "Backup passphrase: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(stderr)
		if err != nil {
			return nil, errors.New("cannot read the passphrase")
		}
		return backupcrypt.PassphraseIdentity(string(b))
	}, nil
}

// trimOneNewline drops one trailing \n or \r\n, as a file or echo leaves it.
func trimOneNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// fetchOffsite downloads the backup named by --from-offsite into the backups
// directory, with its checksum checked against the one recorded in the bucket.
// cleanup removes the file, and the directory when this call created it, so a
// dry run leaves nothing behind.
func fetchOffsite(ctx context.Context, a restoreArgs) (path string, cleanup func(), err error) {
	t, err := a.offsite.target()
	if err != nil {
		return "", nil, err
	}
	client, err := t.Client(nil)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(config.ResolveDataDir(), "backups")
	_, statErr := os.Stat(dir)
	path, err = offsite.Download(ctx, client, t.Prefix, a.fromOffsite, dir)
	if err != nil {
		if statErr != nil {
			_ = os.Remove(dir) // empty: only created for this download
		}
		return "", nil, err
	}
	return path, func() {
		_ = os.Remove(path)
		if statErr != nil {
			_ = os.Remove(dir) // fails, harmlessly, when something else is in it
		}
	}, nil
}
