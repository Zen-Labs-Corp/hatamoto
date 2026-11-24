//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
)

// kitRegistry serves a signed registry that lists the community kit with its
// plugin and theme, and points the CLI at it.
func kitRegistry(t *testing.T) {
	t.Helper()
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{}
	var idx packages.Index
	now := time.Now().UTC()
	idx.Version = 1
	idx.IssuedAt = now.Add(-time.Minute).Format(time.RFC3339)
	idx.ExpiresAt = now.Add(24 * time.Hour).Format(time.RFC3339)
	publish := func(kind packages.Kind, name, version string, files map[string][]byte) {
		raw, err := packages.PackFiles(files, &key)
		if err != nil {
			t.Fatal(err)
		}
		p, err := packages.Parse(raw, packages.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		sum := p.SHA256()
		path := "/" + name + "-" + string(kind) + ".hmp"
		blobs[path] = raw
		idx.Packages = append(idx.Packages, packages.Entry{Kind: kind, Name: name, Version: version, URL: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	}
	publish(packages.KindPlugin, "community-plug", "1.2.0", map[string][]byte{
		"plugin.json": []byte(`{"name":"community-plug","version":"1.2.0","sdk":"^0.27","permissions":["db","settings","site.scripts"]}`),
		"plugin.wasm": []byte("\x00asm\x01\x00\x00\x00"),
	})
	publish(packages.KindTheme, "community-look", "1.0.0", map[string][]byte{"theme.json": []byte(`{"name":"community-look","version":"1.0.0"}`)})
	kitJSON, err := os.ReadFile("../internal/kit/testdata/community/kit.json")
	if err != nil {
		t.Fatal(err)
	}
	publish(packages.KindKit, "community", "1.0.0", map[string][]byte{"kit.json": kitJSON})
	// The same kit under another name, asking for the community policy.
	policyJSON := strings.Replace(string(kitJSON), `"name": "community",`, `"name": "community-policy",`, 1)
	policyJSON = strings.Replace(policyJSON, `"kit": 1,`, `"kit": 1, "requires": ["comment.policy"],`, 1)
	if policyJSON == string(kitJSON) || !strings.Contains(policyJSON, "comment.policy") {
		t.Fatal("the fixture kit.json changed shape")
	}
	publish(packages.KindKit, "community-policy", "1.0.0", map[string][]byte{"kit.json": []byte(policyJSON)})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/registry.json" {
			b, _ := jsonx.Marshal(idx)
			doc, err := packages.SignIndex(b, key)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
			return
		}
		if b, ok := blobs[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	saved := registryClientOptions
	registryClientOptions.AllowPrivate = true
	t.Cleanup(func() { registryClientOptions = saved })
	t.Setenv("HATAMOTO_REGISTRY_URL", srv.URL+"/registry.json")
	t.Setenv("HATAMOTO_REGISTRY_ALLOW_HTTP", "true")
	t.Setenv("HATAMOTO_TRUSTED_KEYS", key.Public)
	t.Setenv("HATAMOTO_DEVELOPER_REGISTRY_URL", "off")
}

func kitCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runKit(context.Background(), args, quietLogger(), &out)
	return out.String(), err
}

func TestKitApplyList_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("PUBLIC_BASE_URL", "")
	kitRegistry(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// No address, flag given: refused before anything is created.
	out, err := kitCLI(t, "apply", "community", "--accept-comment-warnings", "--activate-theme", "--homepage")
	if err == nil || !strings.Contains(out, comment.NoAddressMsg) {
		t.Fatalf("no address: err = %v, out:\n%s", err, out)
	}
	if n := count(`SELECT count(*) FROM hatamoto_applied_kits`); n != 0 {
		t.Fatalf("a refused apply recorded %d kits", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entity_schemas WHERE slug = 'story'`); n != 0 {
		t.Fatal("a refused apply created the content type")
	}

	// Without the flag: everything but comments.
	out, err = kitCLI(t, "apply", "community", "--activate-theme", "--homepage")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	for _, want := range []string{"kit community 1.0.0", comment.EnableWarning, comment.EnableConfirmWarning, "comments left off: pass --accept-comment-warnings", "recorded kit community 1.0.0", "applied."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := count(`SELECT count(*) FROM hatamoto_applied_kits WHERE name = 'community'`); n != 1 {
		t.Fatalf("applied rows = %d", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entity_schemas WHERE slug = 'story'`); n != 1 {
		t.Fatal("the content type was not created")
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'kit.apply' AND actor_kind = 'cli'`); n != 1 {
		t.Errorf("kit.apply audit rows = %d", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'comments.enable'`); n != 0 {
		t.Errorf("comments were turned on without the flag (%d audit rows)", n)
	}

	// With the flag and an address: comments go on, audited, once.
	t.Setenv("PUBLIC_BASE_URL", "https://example.test")
	out, err = kitCLI(t, "apply", "community", "--accept-comment-warnings")
	if err != nil {
		t.Fatalf("apply with flag: %v\n%s", err, out)
	}
	t.Logf("kit apply community --accept-comment-warnings:\n%s", out)
	for _, want := range []string{comment.EnableWarning, comment.EnableConfirmWarning, "accepted with --accept-comment-warnings", "switch   comments on"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'comments.enable'`); n != 1 {
		t.Errorf("comments.enable audit rows = %d", n)
	}

	// Again: nothing to change.
	out, err = kitCLI(t, "apply", "community", "--accept-comment-warnings")
	if err != nil || !strings.Contains(out, "kit community 1.0.0 is already applied: nothing to change") {
		t.Fatalf("second apply: %v\n%s", err, out)
	}

	out, err = kitCLI(t, "list")
	if err != nil || !strings.Contains(out, "community  v1.0.0  applied") || !strings.Contains(out, "(applied)") {
		t.Fatalf("list: %v\n%s", err, out)
	}
}

func TestKitApplyPolicy_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("PUBLIC_BASE_URL", "https://example.test")
	kitRegistry(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	policyOf := func() (order string, p comment.Policy) {
		t.Helper()
		var raw []byte
		if err := db.Pool().QueryRow(ctx, `SELECT settings FROM hatamoto_entity_schemas WHERE slug = 'story'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var st struct {
			Comments struct {
				Order  string         `json:"order"`
				Policy comment.Policy `json:"policy"`
			} `json:"comments"`
		}
		if err := jsonx.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		return st.Comments.Order, st.Comments.Policy
	}

	// Without the flag: the order and the numbers are stored, the policy stays off.
	out, err := kitCLI(t, "apply", "community-policy")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, comment.PolicyConfirmWarning) || !strings.Contains(out, "Community policy left off") {
		t.Errorf("output:\n%s", out)
	}
	if order, p := policyOf(); order != "rank" || p != (comment.Policy{MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}) {
		t.Errorf("stored without the flag: %q %+v", order, p)
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'comments.policy'`); n != 0 {
		t.Errorf("comments.policy audit rows = %d without the flag", n)
	}

	// With the flag: the policy goes on, audited once.
	out, err = kitCLI(t, "apply", "community-policy", "--accept-comment-warnings")
	if err != nil {
		t.Fatalf("apply with flag: %v\n%s", err, out)
	}
	t.Logf("kit apply community-policy --accept-comment-warnings:\n%s", out)
	for _, want := range []string{comment.PolicyConfirmWarning, "accepted with --accept-comment-warnings", "switch   community policy on story", "Community policy is on for story."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if order, p := policyOf(); order != "rank" || p != (comment.Policy{Enabled: true, MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}) {
		t.Errorf("stored with the flag: %q %+v", order, p)
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'comments.policy' AND actor_kind = 'cli'`); n != 1 {
		t.Errorf("comments.policy audit rows = %d", n)
	}
	audits := count(`SELECT count(*) FROM hatamoto_admin_audit`)

	// Again: nothing to change and nothing recorded.
	out, err = kitCLI(t, "apply", "community-policy", "--accept-comment-warnings")
	if err != nil || !strings.Contains(out, "is already applied: nothing to change") || !strings.Contains(out, "kept     community policy on story (already on)") {
		t.Fatalf("second apply: %v\n%s", err, out)
	}
	if n := count(`SELECT count(*) FROM hatamoto_admin_audit`); n != audits {
		t.Errorf("a second apply wrote %d audit rows", n-audits)
	}
}
