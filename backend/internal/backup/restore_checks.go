package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	mediaPrefix = "media/" // entries of content.tar.gz that are uploaded files
	contentName = "content.tar.gz"
	coreName    = "core.dump"
	maxSmall    = 1 << 20 // manifest and secrets.env are read into memory
)

// scanned is what one pass over the archive learned.
type scanned struct {
	mf         manifest
	sums       map[string]part // by entry name: size and sha256 of the bytes in the tar
	secrets    []byte
	hasSecrets bool
	mediaFiles int
	mediaBytes int64
	badMedia   []string // media entry names that would escape the media dir
	contentErr error    // content.tar.gz is not a readable gzip tar
}

// scanArchive reads the archive once, hashing every entry, and reads the
// manifest (its last entry) and secrets.env into memory. Nothing is written.
func scanArchive(file string) (*scanned, error) {
	f, err := os.Open(file) // #nosec G304 -- operator supplied path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := &scanned{sums: map[string]part{}}
	var haveManifest bool
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s is not a readable backup: %w", file, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		hash := sha256.New()
		var n int64
		switch h.Name {
		case manifestName:
			body, err := io.ReadAll(io.LimitReader(tr, maxSmall))
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &s.mf); err != nil {
				return nil, fmt.Errorf("manifest.json is not valid: %w", err)
			}
			haveManifest = true
			continue
		case secretsName:
			var buf bytes.Buffer
			n, err = io.Copy(io.MultiWriter(hash, &buf), io.LimitReader(tr, maxSmall+1))
			if err == nil && n > maxSmall {
				err = errors.New("secrets.env is too large")
			}
			s.secrets, s.hasSecrets = buf.Bytes(), true
		case contentName:
			cr := &countingReader{r: io.TeeReader(tr, hash)}
			s.scanContent(cr)
			_, err = io.Copy(io.Discard, cr) // the rest of the entry, so the hash covers it all
			n = cr.n
		default:
			n, err = io.Copy(hash, tr)
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", h.Name, err)
		}
		s.sums[h.Name] = part{Name: h.Name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	if !haveManifest {
		return nil, fmt.Errorf("%s has no manifest.json; it is not a hatamoto backup", file)
	}
	if s.mf.Format != manifestFormat {
		return nil, fmt.Errorf("%s is not a hatamoto backup (format %q)", file, s.mf.Format)
	}
	if s.mf.Version > manifestVersion {
		return nil, fmt.Errorf("backup manifest version %d is newer than this binary reads (%d)", s.mf.Version, manifestVersion)
	}
	return s, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// scanContent counts the uploaded files in content.tar.gz and notes the names
// that would land outside the media dir.
func (s *scanned) scanContent(r io.Reader) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		s.contentErr = err
		return
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			s.contentErr = err
			return
		}
		key, ok := strings.CutPrefix(h.Name, mediaPrefix)
		if !ok || h.Typeflag != tar.TypeReg {
			continue
		}
		if !safeKey(key) {
			s.badMedia = append(s.badMedia, h.Name)
			continue
		}
		s.mediaFiles++
		s.mediaBytes += h.Size
	}
}

// safeKey reports whether a media key stays inside the media dir: relative,
// clean, no "..", no backslash.
func safeKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, "\\\x00") && path.Clean(key) == key &&
		!strings.HasPrefix(key, "/") && key != ".." && !strings.HasPrefix(key, "../")
}

// check runs every check, collects the refusals and builds the plan. Only a
// failure to run a check at all (database unreachable) is a plain error.
func (x *restorer) check(ctx context.Context) (*RestorePlan, error) {
	q, closeQ, err := x.querier(ctx)
	if err != nil {
		return nil, err
	}
	defer closeQ()

	var refuse []string
	plan := &RestorePlan{}
	mf, s := x.mf, x.scan

	// Parts: every one the manifest lists is in the archive and matches.
	if bad := x.checkParts(); len(bad) > 0 {
		refuse = append(refuse, bad...)
	}
	if s.contentErr != nil {
		refuse = append(refuse, "content.tar.gz is not readable: "+s.contentErr.Error())
	}
	for _, name := range s.badMedia {
		refuse = append(refuse, fmt.Sprintf("media entry %q would write outside the media dir", name))
	}

	refuse = append(refuse, x.ambientLibpqProblems()...)

	// The config target, when the operator names one.
	if reason := configOutProblem(x.req.ConfigOut); reason != "" {
		refuse = append(refuse, reason)
	}

	// Version.
	switch {
	case mf.HatamotoVersion == "":
		refuse = append(refuse, "the backup records no hatamoto version; run the release that made it, restore, then update")
	case x.o.Version == "":
		refuse = append(refuse, fmt.Sprintf("this binary has no version; run the release that made the backup (%q)", mf.HatamotoVersion))
	case mf.HatamotoVersion != x.o.Version:
		refuse = append(refuse, fmt.Sprintf("backup is from hatamoto %q, this is %s; run the matching release, restore, then update", mf.HatamotoVersion, x.o.Version))
	default:
		plan.Checks = append(plan.Checks, "version ok")
	}

	// Postgres majors: the server and both client tools.
	pgOK := true
	server, err := x.serverMajor(ctx, q)
	switch {
	case err != nil:
		return nil, fmt.Errorf("cannot read the server version: %w", err)
	case server != mf.ServerMajor:
		pgOK = false
		refuse = append(refuse, fmt.Sprintf("backup is from PostgreSQL %d, the server is %d", mf.ServerMajor, server))
	}
	for _, tool := range []string{"pg_restore", "psql"} {
		major, err := toolMajor(ctx, x.run, tool)
		switch {
		case err != nil:
			pgOK = false
			refuse = append(refuse, err.Error())
		case major != mf.ServerMajor:
			pgOK = false
			refuse = append(refuse, fmt.Sprintf("%s %d does not match the backup's PostgreSQL %d", tool, major, mf.ServerMajor))
		}
	}
	if pgOK {
		plan.Checks = append(plan.Checks, "pg major ok")
	}

	// Trust keys.
	trustReasons, trustWarn, trustOK, err := x.checkTrust()
	if err != nil {
		return nil, err
	}
	refuse = append(refuse, trustReasons...)
	plan.Warnings = append(plan.Warnings, trustWarn...)
	if trustOK {
		plan.Checks = append(plan.Checks, "trust keys ok")
	}

	// The superuser URL, before anything is written: the bootstrap would
	// otherwise fail after the core restore.
	if reasons := x.checkAdmin(ctx, q); len(reasons) > 0 {
		refuse = append(refuse, reasons...)
	} else {
		plan.Checks = append(plan.Checks, "admin url ok")
	}

	// The target database holds nothing yet.
	schemas, err := nonEmptySchemas(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cannot inspect the target database: %w", err)
	}
	if len(schemas) > 0 {
		refuse = append(refuse, "the target database is not empty (relations in: "+strings.Join(schemas, ", ")+")")
	} else {
		plan.Checks = append(plan.Checks, "target db empty")
	}

	// The media dir holds nothing yet.
	switch entries, err := os.ReadDir(x.o.MediaDir); {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("cannot read the media dir: %w", err)
	case len(entries) > 0:
		refuse = append(refuse, fmt.Sprintf("the media dir %s is not empty", x.o.MediaDir))
	default:
		plan.Checks = append(plan.Checks, "media dir empty")
	}

	// secrets.env: an existing one must be the same file.
	if s.hasSecrets && x.o.SecretsDir != "" {
		have, err := os.ReadFile(filepath.Join(x.o.SecretsDir, secretsName)) // #nosec G304 -- operator supplied dir
		switch {
		case err == nil && !bytes.Equal(have, s.secrets):
			refuse = append(refuse, fmt.Sprintf("%s already holds a different secrets.env; it is never overwritten", x.o.SecretsDir))
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("cannot read the existing secrets.env: %w", err)
		}
	}

	if len(refuse) > 0 {
		return nil, &RefusalError{Reasons: refuse}
	}
	x.planConfig(plan)
	x.buildPlan(plan)
	return plan, nil
}

// checkAdmin checks what the real run's database steps would need: the
// DATABASE_URL names an app role and a database, the admin URL is a postgres
// URL that connects to that database as a superuser, and both reach the same
// cluster (roles are cluster wide, so a bootstrap elsewhere would not help).
func (x *restorer) checkAdmin(ctx context.Context, appQ Querier) []string {
	if _, err := pgEnv("DATABASE_URL", x.o.DatabaseURL, true); err != nil {
		return []string{err.Error()}
	}
	target := &dbRestorer{restorer: x}
	if err := target.parseTarget(); err != nil {
		return []string{err.Error()}
	}
	if x.req.AdminURL == "" {
		return []string{"no admin URL; pass --admin-url"}
	}
	if _, err := pgEnv("--admin-url", x.req.AdminURL, true); err != nil {
		return []string{err.Error()}
	}
	au, _ := url.Parse(x.req.AdminURL) // parsed by pgEnv above
	// The bootstrap runs in the target database; a dbname in the admin URL's
	// query would override the path, so drop it.
	aq := au.Query()
	aq.Del("dbname")
	au.RawQuery = aq.Encode()
	au.Path, au.RawPath = "/"+target.dbName, ""
	open := x.o.OpenAdmin
	if open == nil {
		open = openAdminConn
	}
	adminQ, closeAdmin, err := open(ctx, au.String())
	if err != nil {
		return []string{"--admin-url cannot connect to the target database: " + errorLine(err)}
	}
	defer closeAdmin()
	var super bool
	if err := adminQ.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		return []string{"cannot check the --admin-url role: " + errorLine(err)}
	}
	if !super {
		return []string{"--admin-url is not a superuser; the role bootstrap needs one"}
	}
	adminID, appID, err := clusterIDs(ctx, adminQ, appQ)
	if err != nil {
		return []string{"cannot tell whether --admin-url and DATABASE_URL reach the same cluster: " + errorLine(err)}
	}
	if adminID != appID {
		return []string{"--admin-url reaches a different PostgreSQL cluster than DATABASE_URL"}
	}
	return nil
}

// errorLine keeps a connection error to one line (and out of the URL).
func errorLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// clusterIDs reads the identity of the cluster behind each connection: the
// control file's system_identifier when the app role may read it, else the
// server address, port and postmaster start time, which two clusters never
// share.
func clusterIDs(ctx context.Context, adminQ, appQ Querier) (adminID, appID string, err error) {
	const sysID = `SELECT system_identifier::text FROM pg_control_system()`
	const instance = `SELECT coalesce(inet_server_addr()::text, '') || ':' || coalesce(inet_server_port()::text, '') || '@' || pg_postmaster_start_time()::text`
	if adminID, err = scalar(ctx, adminQ, sysID); err != nil {
		return "", "", err
	}
	if appID, err = scalar(ctx, appQ, sysID); err == nil {
		return adminID, appID, nil
	}
	if adminID, err = scalar(ctx, adminQ, instance); err != nil {
		return "", "", err
	}
	appID, err = scalar(ctx, appQ, instance)
	return adminID, appID, err
}

func scalar(ctx context.Context, q Querier, sql string) (string, error) {
	var s string
	err := q.QueryRow(ctx, sql).Scan(&s)
	return s, err
}

// openAdminConn is the production OpenAdmin.
func openAdminConn(ctx context.Context, raw string) (Querier, func(), error) {
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close(ctx) }, nil
}

// checkParts compares the manifest's parts with what the archive holds.
func (x *restorer) checkParts() []string {
	var bad []string
	listed := map[string]bool{}
	for _, p := range x.mf.Parts {
		listed[p.Name] = true
		got, ok := x.scan.sums[p.Name]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("part %s is missing from the archive", p.Name))
		case got.SHA256 != p.SHA256 || got.Size != p.Size:
			bad = append(bad, fmt.Sprintf("part %s does not match its checksum; the archive is damaged", p.Name))
		}
	}
	if !listed[coreName] {
		bad = append(bad, "the backup has no "+coreName)
	}
	for _, name := range x.mf.Plugins {
		if !schemaPattern.MatchString(schemaName(name)) || !listed[schemaName(name)+".dump"] {
			bad = append(bad, fmt.Sprintf("plugin %q has no usable dump in the backup", name))
		}
	}
	return bad
}

// checkTrust refuses when the embedded release or manifest key IDs differ from
// the backup's; other differences and missing records are warnings.
func (x *restorer) checkTrust() (reasons, warnings []string, ok bool, err error) {
	have := x.mf.Trust
	if have == nil {
		return nil, []string{"the backup records no trust key IDs (made before they were recorded)"}, false, nil
	}
	fn := x.o.Trust
	if fn == nil {
		fn = embeddedTrust
	}
	now, err := fn()
	if err != nil {
		return nil, nil, false, fmt.Errorf("trust keys: %w", err)
	}
	ok = true
	for _, c := range []struct {
		name      string
		was, here []string
	}{{"release", have.Release, now.Release}, {"manifest", have.Manifest, now.Manifest}} {
		if !sameIDs(c.was, c.here) {
			ok = false
			reasons = append(reasons, fmt.Sprintf("the %s signing keys of this binary (%s) differ from the backup's (%s)",
				c.name, idList(c.here), idList(c.was)))
		}
	}
	if !sameIDs(have.Env, now.Env) {
		warnings = append(warnings, fmt.Sprintf("HATAMOTO_TRUSTED_KEYS differs from the backup (backup: %s; here: %s); the restored config carries the backup's",
			idList(have.Env), idList(now.Env)))
	}
	return reasons, warnings, ok, nil
}

func sameIDs(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func idList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, " ")
}

// planConfig decides where the config would go and warns about the parts an
// old archive lacks.
func (x *restorer) planConfig(plan *RestorePlan) {
	if !x.scan.hasSecrets {
		plan.Warnings = append(plan.Warnings, "no secrets.env in the backup; set HATAMOTO_SECRET yourself")
	}
	if _, ok := x.scan.sums[configName]; !ok {
		plan.Warnings = append(plan.Warnings, "no config in the backup; write the environment file yourself")
		return
	}
	target := x.req.ConfigOut
	switch _, err := os.Lstat(target); {
	case target == "":
		// The recorded path is only a hint: a backup archive from elsewhere
		// must not choose where this machine writes a file.
		if p := x.mf.ConfigPath; p != "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("config not written; recorded path %q; pass --config-out with the path you want", p))
		} else {
			plan.Warnings = append(plan.Warnings, "config not written; the backup records no config path; pass --config-out to write it")
		}
	case err == nil:
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s exists and is not overwritten; the config is not written (use --config-out)", target))
	default:
		x.configTarget = target
	}
}

// configOutProblem says why --config-out cannot be used: it must be an
// absolute, clean path whose parent directory exists (restore never creates
// directories for it). Empty means usable, or not given.
func configOutProblem(p string) string {
	switch {
	case p == "":
		return ""
	case !filepath.IsAbs(p) || filepath.Clean(p) != p:
		return fmt.Sprintf("--config-out %q must be an absolute, clean path", p)
	}
	if fi, err := os.Stat(filepath.Dir(p)); err != nil || !fi.IsDir() {
		return fmt.Sprintf("--config-out: the directory %s does not exist", filepath.Dir(p))
	}
	return ""
}

// buildPlan lists what the real run does, in order.
func (x *restorer) buildPlan(plan *RestorePlan) {
	mf := x.mf
	n := len(mf.Plugins)
	plan.Backup = fmt.Sprintf("%s (hatamoto %q, pg %d, %d plugin%s)",
		NewID(mf.Created, mf.Kind), mf.HatamotoVersion, mf.ServerMajor, n, plural(n))
	plan.Would = []string{"restore " + coreName, "run role bootstrap"}
	for _, name := range mf.Plugins {
		plan.Would = append(plan.Would, "provision "+schemaName(name))
	}
	for _, name := range mf.Plugins {
		plan.Would = append(plan.Would, "restore "+schemaName(name)+".dump")
	}
	plan.Would = append(plan.Would, fmt.Sprintf("restore %d media file%s (%s)", x.scan.mediaFiles, plural(x.scan.mediaFiles), mib(x.scan.mediaBytes)))
	if x.scan.hasSecrets {
		plan.Would = append(plan.Would, "write secrets.env")
	}
	if x.configTarget != "" {
		plan.Would = append(plan.Would, "write config to "+x.configTarget)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

// querier is the target database reader: Options.Q, or a pool opened from the
// database URL for the length of the checks.
func (x *restorer) querier(ctx context.Context) (Querier, func(), error) {
	if x.o.Q != nil {
		return x.o.Q, func() {}, nil
	}
	if x.o.DatabaseURL == "" {
		return nil, nil, errors.New("DATABASE_URL is not set")
	}
	open := x.o.OpenPool
	if open == nil {
		open = func(ctx context.Context, url string) (*pgxpool.Pool, error) {
			pool, err := pgxpool.New(ctx, url)
			if err != nil {
				return nil, err
			}
			if err := pool.Ping(ctx); err != nil {
				pool.Close()
				return nil, err
			}
			return pool, nil
		}
	}
	pool, err := open(ctx, x.o.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot reach the target database: %w", err)
	}
	return pool, pool.Close, nil
}

func (x *restorer) serverMajor(ctx context.Context, q Querier) (int, error) {
	if x.o.ServerMajor != nil {
		return x.o.ServerMajor(ctx)
	}
	var n int
	err := q.QueryRow(ctx, `SELECT current_setting('server_version_num')::int / 10000`).Scan(&n)
	return n, err
}

// toolMajor is the major version a client tool reports.
func toolMajor(ctx context.Context, run ExecFunc, tool string) (int, error) {
	out, err := run(ctx, nil, tool, "--version")
	if err != nil {
		return 0, fmt.Errorf("%s is not usable: %w", tool, err)
	}
	m := pgDumpVersion.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return 0, fmt.Errorf("cannot read the %s version from %q", tool, strings.TrimSpace(string(out)))
	}
	return strconv.Atoi(m[1])
}

// nonEmptySchemas names the schemas that hold relations outside the system
// schemas; hatamoto_admin counts, because an early bootstrap would make the
// instance id disagree with the restored one.
func nonEmptySchemas(ctx context.Context, q Querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT n.nspname
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p','v','m','S','f')
		  AND n.nspname NOT IN ('pg_catalog','information_schema')
		  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ambientLibpqVars are libpq settings that make psql and pg_restore reach a
// server other than the one the checks (pgx) connect to. The service file beats
// the PGHOST, PGPORT, PGUSER and PGDATABASE that restore exports, while pgx
// lets the URL beat it, and PGHOSTADDR overrides host resolution in libpq only.
var ambientLibpqVars = []string{"PGSERVICE", "PGSERVICEFILE", "PGHOSTADDR"}

func (x *restorer) ambientLibpqProblems() []string {
	getenv := x.o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var out []string
	for _, name := range ambientLibpqVars {
		if getenv(name) != "" {
			out = append(out, fmt.Sprintf("%s is set in the environment; psql and pg_restore would reach a different server than the one checked, so unset it and run again", name))
		}
	}
	return out
}
