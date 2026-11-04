//go:build integration

package standing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func newStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return &Store{DB: db}, ctx
}

func testPlugin(t *testing.T, s *Store, ctx context.Context) string {
	name := "standing-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_standing_approvals WHERE plugin = $1`, name)
	})
	return name
}

func samplePolicy() Policy {
	return Policy{Fields: []string{"price"}, Schemas: []string{"product"}, MaxPerHour: 200}
}

func sampleFP(version string) Fingerprint {
	return Fingerprint{Version: version, ManifestHash: "m1", SchemaHashes: map[string]string{"product": "s1"}}
}

func TestApproveReplacesTheActiveRow(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	if _, err := s.Active(ctx, p, "sync"); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("Active before approving = %v, want ErrNoApproval", err)
	}
	first, err := s.Approve(ctx, p, "sync", samplePolicy(), sampleFP("1.0.0"), "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Approve(ctx, p, "sync", samplePolicy(), sampleFP("1.1.0"), "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("second approval reused the first row")
	}
	got, err := s.Active(ctx, p, "sync")
	if err != nil || got.ID != second.ID || got.FP.Version != "1.1.0" || got.ApprovedBy != "owner@example.com" {
		t.Fatalf("Active = %+v, %v; want the second approval", got, err)
	}
	if got.Policy.MaxPerHour != 200 || got.Policy.Fields[0] != "price" {
		t.Fatalf("policy round trip: %+v", got.Policy)
	}
	var active int
	var reason string
	if err := s.DB.Pool().QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE voided_at IS NULL), max(void_reason) FROM hatamoto_standing_approvals WHERE plugin = $1`, p).
		Scan(&active, &reason); err != nil {
		t.Fatal(err)
	}
	if active != 1 || reason != "replaced" {
		t.Fatalf("active rows = %d, void reason = %q", active, reason)
	}
}

func TestVoidTxIsIdempotent(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	other := testPlugin(t, s, ctx)
	for _, name := range []string{p, other} {
		if _, err := s.Approve(ctx, name, "sync", samplePolicy(), sampleFP("1.0.0"), ""); err != nil {
			t.Fatal(err)
		}
	}
	void := func() int64 {
		var n int64
		err := pgx.BeginFunc(ctx, s.DB.Pool(), func(tx pgx.Tx) (err error) {
			n, err = VoidTx(ctx, tx, p, "plugin updated")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := void(); n != 1 {
		t.Fatalf("first void = %d, want 1", n)
	}
	if n := void(); n != 0 {
		t.Fatalf("second void = %d, want 0", n)
	}
	if _, err := s.Active(ctx, p, "sync"); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("Active after void = %v, want ErrNoApproval", err)
	}
	if _, err := s.Active(ctx, other, "sync"); err != nil {
		t.Fatalf("another plugin's approval was voided: %v", err)
	}
	// Approving again after a void works and is active.
	if _, err := s.Approve(ctx, p, "sync", samplePolicy(), sampleFP("2.0.0"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(ctx, p, "sync"); err != nil {
		t.Fatal(err)
	}
}

func TestManifestHashIgnoresKeyOrder(t *testing.T) {
	a, err := ManifestHash(json.RawMessage(`{"name":"x","runs":[{"b":1,"a":2}],"version":"1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ManifestHash(json.RawMessage(`{ "version": "1.0.0", "runs": [ {"a": 2, "b": 1} ], "name": "x" }`))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("hash changed with key order: %s vs %s", a, b)
	}
	c, _ := ManifestHash(json.RawMessage(`{"name":"x","runs":[{"b":1,"a":3}],"version":"1.0.0"}`))
	if a == c {
		t.Fatal("hash ignored a value change")
	}
	if _, err := ManifestHash(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON hashed")
	}
}

func TestApprovalMatches(t *testing.T) {
	a := Approval{Policy: samplePolicy(), FP: sampleFP("1.0.0")}
	cases := []struct {
		name string
		cur  Fingerprint
		want bool
	}{
		{"same", sampleFP("1.0.0"), true},
		{"version", sampleFP("1.0.1"), false},
		{"manifest", Fingerprint{Version: "1.0.0", ManifestHash: "m2", SchemaHashes: map[string]string{"product": "s1"}}, false},
		{"schema changed", Fingerprint{Version: "1.0.0", ManifestHash: "m1", SchemaHashes: map[string]string{"product": "s2"}}, false},
		{"schema missing", Fingerprint{Version: "1.0.0", ManifestHash: "m1"}, false},
	}
	for _, tc := range cases {
		if got := a.Matches(tc.cur); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLastVoided(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	if v, err := s.LastVoided(ctx, p, "sync"); err != nil || v != nil {
		t.Fatalf("before any approval = %+v, %v", v, err)
	}
	if _, err := s.Approve(ctx, p, "sync", samplePolicy(), sampleFP("1.0.0"), ""); err != nil {
		t.Fatal(err)
	}
	if v, err := s.LastVoided(ctx, p, "sync"); err != nil || v != nil {
		t.Fatalf("while active = %+v, %v", v, err)
	}
	err := pgx.BeginFunc(ctx, s.DB.Pool(), func(tx pgx.Tx) error {
		_, err := VoidTx(ctx, tx, p, "plugin updated")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.LastVoided(ctx, p, "sync"); err != nil || v == nil || v.Reason != "plugin updated" {
		t.Fatalf("after a void = %+v, %v", v, err)
	}
	if _, err := s.Approve(ctx, p, "sync", samplePolicy(), sampleFP("2.0.0"), ""); err != nil {
		t.Fatal(err)
	}
	if v, err := s.LastVoided(ctx, p, "sync"); err != nil || v != nil {
		t.Fatalf("after approving again = %+v, %v", v, err)
	}
}

func TestVoidEndsTheActiveApproval(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	if _, err := s.Approve(ctx, p, "sync", samplePolicy(), sampleFP("1.0.0"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Void(ctx, p, "schema changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(ctx, p, "sync"); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("active after a void: %v", err)
	}
	if v, err := s.LastVoided(ctx, p, "sync"); err != nil || v == nil || v.Reason != "schema changed" {
		t.Fatalf("last void = %+v, %v", v, err)
	}
}
