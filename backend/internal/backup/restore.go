package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/backupcrypt"
	"hatamoto.dev/core/internal/media"
)

// RestoreOptions say where a backup is restored to. Everything is checked
// against the archive before anything is written.
type RestoreOptions struct {
	Data     string // the data dir; an id source is $Data/backups/<id>.tar
	MediaDir string // must be empty or missing
	// MediaStore holds the media bytes when they are in object storage; nil
	// means MediaDir. An archive with media bytes is restored into it (the
	// prefix must be empty); one with only a media manifest is checked
	// against it.
	MediaStore  media.BlobStore
	SecretsDir  string // secrets.env is written here
	DatabaseURL string // the app role on the empty target database
	Version     string // the running hatamoto version; must equal the backup's
	Run         ExecFunc
	// Q reads the target database (emptiness, server major). Nil opens a pool
	// from DatabaseURL for the checks.
	Q           Querier
	ServerMajor func(ctx context.Context) (int, error)
	// Trust gives the key IDs of this binary; nil means the embedded ones.
	Trust    func() (TrustIDs, error)
	OpenPool func(ctx context.Context, url string) (*pgxpool.Pool, error)
	// PauseOffsite runs the SQL that switches off-site backups off, as the app
	// role; nil opens a pool from DatabaseURL.
	PauseOffsite func(ctx context.Context, sql string) error
	// OpenAdmin connects with the admin URL (pointed at the target database)
	// for the pre-flight checks; nil connects with pgx.
	OpenAdmin func(ctx context.Context, url string) (Querier, func(), error)
	Log       *slog.Logger
	// Getenv reads the ambient environment for the libpq overrides check; nil
	// means os.Getenv.
	Getenv func(string) string
}

// RestoreRequest is one restore.
type RestoreRequest struct {
	Source    string // a backup id or the path of a .tar or .tar.age
	AdminURL  string // DSN of a superuser, or of a role with CREATEROLE, CREATEDB and CREATE on the target database, for the role bootstrap
	ConfigOut string // where to write the recorded config; empty means the config is not written
	DryRun    bool
	// Force restores a backup made under a different server secret; its commenter emails are blanked.
	Force bool
	// Identity returns the private key or passphrase for an age-encrypted
	// source. It is called only when the source is encrypted; nil means none.
	Identity func() (*backupcrypt.Identity, error)
}

// RestorePlan is what a restore checked and does (or would do).
type RestorePlan struct {
	Backup   string
	Checks   []string
	Would    []string
	Warnings []string
	DryRun   bool
	Done     bool // the steps ran: Print says "done", not "would"
}

// RefusalError lists every reason a restore was refused; nothing was written.
type RefusalError struct{ Reasons []string }

func (e *RefusalError) Error() string {
	return "restore refused:\n  - " + strings.Join(e.Reasons, "\n  - ")
}

// restorer is one restore in progress.
type restorer struct {
	o   RestoreOptions
	req RestoreRequest
	run ExecFunc
	log *slog.Logger

	src          string
	scan         *scanned
	mf           manifest
	configTarget string // empty when the config will not be written
	led          rollbackLedger
	ana          *roleAnalysis // the real run's role analysis, set by dbSteps
	dropEmails   bool          // set by check: Force over a secret mismatch
}

// Restore checks a backup against this machine and, unless it is a dry run,
// restores it. It is not an Engine method: Engine.New creates directories, and
// a dry run writes nothing.
func Restore(ctx context.Context, o RestoreOptions, req RestoreRequest) (*RestorePlan, error) {
	x := &restorer{o: o, req: req, run: o.Run, log: o.Log}
	if x.run == nil {
		x.run = OSExec
	}
	if x.log == nil {
		x.log = slog.Default()
	}
	src, err := resolveSource(o.Data, req.Source)
	if err != nil {
		return nil, err
	}
	if enc, err := backupcrypt.IsEncrypted(src); err != nil {
		return nil, err
	} else if enc {
		tmp, err := x.decrypt(src)
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp)
		src = tmp
	}
	x.src = src
	if x.scan, err = scanArchive(src); err != nil {
		return nil, err
	}
	x.mf = x.scan.mf
	plan, err := x.check(ctx)
	if err != nil {
		return nil, err
	}
	plan.DryRun = req.DryRun
	if req.DryRun {
		return plan, nil
	}
	if err := x.dbSteps(ctx); err != nil {
		return plan, x.rollback(ctx, err)
	}
	if err := x.fileSteps(ctx, plan); err != nil {
		return plan, x.rollback(ctx, err)
	}
	// The grants were read again for the run; roles may have changed since the checks.
	if x.ana != nil {
		plan.Warnings = slices.DeleteFunc(plan.Warnings, func(w string) bool { return strings.HasPrefix(w, "grants to role ") })
		plan.Warnings = append(plan.Warnings, skipWarnings(x.ana)...)
	}
	plan.Done = true
	return plan, nil
}

// decrypt opens an encrypted source once into a temp file next to it (the
// restore reads the tar several times) and returns its path. A wrong key fails
// here, before any archive scan or database access. The caller removes the file.
func (x *restorer) decrypt(src string) (string, error) {
	if x.req.Identity == nil {
		return "", backupcrypt.ErrNoKey
	}
	id, err := x.req.Identity()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(src)
	f, err := os.CreateTemp(dir, ".tmp-decrypt-*.tar")
	if err != nil {
		return "", fmt.Errorf("cannot write the decrypted copy next to the backup in %s: %w", dir, err)
	}
	tmp := f.Name()
	f.Close()
	// DecryptFile creates the file itself (O_EXCL) and removes it on failure.
	if err := os.Remove(tmp); err != nil {
		return "", err
	}
	if err := id.DecryptFile(src, tmp); err != nil {
		return "", err
	}
	return tmp, nil
}

// resolveSource turns an id or a path into the archive path. A source ending
// in .tar or .tar.age or holding a path separator is a path; anything else must be an id.
func resolveSource(data, source string) (string, error) {
	if source == "" {
		return "", errors.New("no backup given")
	}
	path := source
	if !strings.HasSuffix(source, archiveExt) && !strings.HasSuffix(source, archiveExt+".age") && !strings.ContainsRune(source, filepath.Separator) {
		if !ValidID(source) {
			return "", fmt.Errorf("%q is neither a backup id nor a .tar or .tar.age path", source)
		}
		path = filepath.Join(data, "backups", source+archiveExt)
	}
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("backup %s not found (%s)", source, path)
		}
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", path)
	}
	return path, nil
}

// Print writes the plan in the shape of the restore command output.
func (p *RestorePlan) Print(w io.Writer) {
	for _, s := range p.Warnings {
		fmt.Fprintf(w, "warning  %s\n", s)
	}
	fmt.Fprintf(w, "backup   %s\n", p.Backup)
	fmt.Fprintf(w, "checks   %s\n", strings.Join(p.Checks, ", "))
	label := "would    "
	if p.Done {
		label = "done     "
	}
	fmt.Fprint(w, wrapList(label, p.Would))
	switch {
	case p.DryRun:
		fmt.Fprintln(w, "dry run: nothing written")
	case p.Done:
		fmt.Fprintln(w, "restore complete")
	}
}

const wrapWidth = 100

// wrapList joins items with ", " after label, breaking lines at wrapWidth and
// indenting continuation lines under the first item.
func wrapList(label string, items []string) string {
	indent := strings.Repeat(" ", len(label))
	var b strings.Builder
	b.WriteString(label)
	col := len(label)
	for i, it := range items {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		if i > 0 && col+len(sep)+len(it) > wrapWidth {
			b.WriteString(",\n" + indent)
			col = len(indent)
			sep = ""
		} else {
			b.WriteString(sep)
			col += len(sep)
		}
		b.WriteString(it)
		col += len(it)
	}
	b.WriteString("\n")
	return b.String()
}
