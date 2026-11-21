//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/s3/s3test"
	"hatamoto.dev/core/internal/staticexport"
)

// staticCLISite points the process environment at a scratch database holding a
// small site (a home page and one post) and returns the database URL.
func staticCLISite(t *testing.T, accessMode string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	dsn, pool := scratchDB(t)
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	home := uuid.New()
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1,id,'Home','home','published',now(),'{"content":"<p>Welcome. <a href=\"/about\">About</a></p>"}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug='page'`, home)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1,id,'About','about','published',now(),'{"content":"<p>About us.</p>"}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug='page'`, uuid.New())
	// A post keeps the feeds' updated time tied to content, not the clock.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		SELECT $1,id,'Hello','hello','published',now(),'{"content":"<p>Hello.</p>"}'::jsonb
		FROM hatamoto_entity_schemas WHERE slug='post'`, uuid.New())
	exec(`INSERT INTO hatamoto_site_config (company_name, public_base_url, homepage_entity_id, site_access_mode)
		VALUES ('Static CLI IT','https://static-cli.example.com',$1,$2)`, home, accessMode)

	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "static-cli-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Static CLI IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Static CLI IT")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	t.Setenv("HATAMOTO_DATA", t.TempDir())
	t.Setenv("HATAMOTO_ADMIN_PATH", "")
	return pool
}

func TestStaticCLI_DirExportAndFailure(t *testing.T) {
	staticCLISite(t, "open")
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "site")

	var buf bytes.Buffer
	if err := runStatic(ctx, []string{"--out", out}, &buf); err != nil {
		t.Fatalf("first export: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "static: done") {
		t.Fatalf("output = %s", buf.String())
	}
	for _, f := range []string{"index.html", "about/index.html", "404.html", staticexport.ManifestFile} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if _, err := os.Stat(out + ".prev"); !os.IsNotExist(err) {
		t.Fatalf("first export left %s.prev: %v", out, err)
	}

	buf.Reset()
	if err := runStatic(ctx, []string{"--out", out}, &buf); err != nil {
		t.Fatalf("second export: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(filepath.Join(out+".prev", "index.html")); err != nil {
		t.Fatalf("second export did not keep the previous tree: %v", err)
	}
	if !strings.Contains(buf.String(), "changed 0, unchanged") {
		t.Errorf("an unchanged site should report no changes:\n%s", buf.String())
	}

	// A directory that is not an export is refused and left alone.
	foreign := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(foreign, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runStatic(ctx, []string{"--out", foreign}, &buf); err == nil {
		t.Fatal("a foreign directory was accepted")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "mine" {
		t.Fatalf("the foreign directory changed: %q, %v", b, err)
	}
}

func TestStaticCLI_FailureKeepsPublished(t *testing.T) {
	pool := staticCLISite(t, "open")
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "site")
	if err := runStatic(ctx, []string{"--out", out}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	// Switch the gate on: the next export is refused and changes nothing.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_site_config SET site_access_mode='maintenance'`); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = runStatic(ctx, []string{"--out", out}, &buf)
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(buf.String(), "export failed, "+out+" unchanged") || !strings.Contains(buf.String(), "site access gate is on") {
		t.Fatalf("output = %s", buf.String())
	}
	after, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the published tree changed: %v", err)
	}
	if _, err := os.Stat(out + ".prev"); !os.IsNotExist(err) {
		t.Fatalf("a refused export moved the tree aside: %v", err)
	}
}

func TestStaticCLI_S3Env(t *testing.T) {
	staticCLISite(t, "open")
	srv := s3test.NewServer(t)
	t.Setenv(staticexport.EnvEndpoint, srv.URL)
	t.Setenv(staticexport.EnvRegion, s3test.Region)
	t.Setenv(staticexport.EnvBucket, s3test.Bucket)
	t.Setenv(staticexport.EnvPrefix, "site")
	t.Setenv(staticexport.EnvAccessKey, s3test.AccessKey)
	t.Setenv(staticexport.EnvSecretKey, s3test.SecretKey)

	var buf bytes.Buffer
	if err := runStatic(context.Background(), []string{"--target", "s3"}, &buf); err != nil {
		t.Fatalf("export: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), s3test.SecretKey) {
		t.Fatal("the secret key is in the output")
	}
	objs := srv.Objects()
	for _, key := range []string{"site/index.html", "site/about/index.html", "site/404.html", "site/" + staticexport.ManifestFile} {
		if _, ok := objs[key]; !ok {
			t.Errorf("missing object %s (have %d objects)", key, len(objs))
		}
	}
	var sawHTML bool
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/site/index.html") {
			sawHTML = true
			if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Errorf("Content-Type = %q", got)
			}
			if got := r.Header.Get("Cache-Control"); got != "public, max-age=300" {
				t.Errorf("Cache-Control = %q", got)
			}
		}
	}
	if !sawHTML {
		t.Error("index.html was not uploaded")
	}
}
