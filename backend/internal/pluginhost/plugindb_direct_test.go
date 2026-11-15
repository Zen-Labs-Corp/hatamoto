package pluginhost

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// With a base config set, plugin pools start from it (the direct database),
// not from the core pool's (possibly pooled) endpoint.
func TestPluginBaseConfigOverridesCorePool(t *testing.T) {
	core, err := pgxpool.ParseConfig("postgres://app:core@pooler.example.com:6432/x?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := pgxpool.ParseConfig("postgres://app:core@db.example.com:5432/x?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	d := NewPluginDB(pool, nil)

	cc := pluginConnConfig(d.baseConfig(), "capa", "hm_p_1234abcd_capa", "pw")
	if cc.Host != "pooler.example.com" || cc.Port != 6432 {
		t.Fatalf("default base: %s:%d, want the core pool's", cc.Host, cc.Port)
	}

	d.SetBaseConfig(func() *pgx.ConnConfig { return direct.ConnConfig.Copy() })
	cc = pluginConnConfig(d.baseConfig(), "capa", "hm_p_1234abcd_capa", "pw")
	if cc.Host != "db.example.com" || cc.Port != 5432 {
		t.Fatalf("override base: %s:%d, want the direct endpoint", cc.Host, cc.Port)
	}
	if cc.User != "hm_p_1234abcd_capa" || cc.Password != "pw" {
		t.Errorf("user/password = %q/%q, want the plugin role's", cc.User, cc.Password)
	}
}
