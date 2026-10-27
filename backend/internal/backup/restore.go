package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RestoreOptions say where a backup is restored to. Everything is checked
// against the archive before anything is written.
type RestoreOptions struct {
	Data        string // the data dir; an id source is $Data/backups/<id>.tar
	MediaDir    string // must be empty or missing
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
	Source    string // a backup id or the path of a .tar
	AdminURL  string // superuser DSN for the role bootstrap
	ConfigOut string // where to write the recorded config; empty means the config is not written
	DryRun    bool
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
		return plan, err
	}
	if err := x.fileSteps(ctx, plan); err != nil {
		return plan, err
	}
	plan.Done = true
	return plan, nil
}

// resolveSource turns an id or a path into the archive path. A source ending
// in .tar or holding a path separator is a path; anything else must be an id.
func resolveSource(data, source string) (string, error) {
	if source == "" {
		return "", errors.New("no backup given")
	}
	path := source
	if !strings.HasSuffix(source, archiveExt) && !strings.ContainsRune(source, filepath.Separator) {
		if !ValidID(source) {
			return "", fmt.Errorf("%q is neither a backup id nor a .tar path", source)
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
