//go:build integration

package fonts

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

func scratchStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_fonts_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	p, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(pool)
}

func newFont(family string, weight int, data string) NewFont {
	b := []byte("wOF2" + data)
	return NewFont{Family: family, Weight: weight, Style: "normal", Source: "upload", Hash: hashOf(b), Data: b}
}

func TestStoreRoundTrip(t *testing.T) {
	s := scratchStore(t)
	ctx := context.Background()
	a, b := newFont("Brand Sans", 400, "a"), newFont("Brand Sans", 700, "b")
	if err := s.Insert(ctx, []NewFont{a, b, newFont("Alpha", 400, "c")}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 3 || list[0].Family != "Alpha" || list[1].Weight != 400 || list[2].Weight != 700 {
		t.Fatalf("List = %+v err %v", list, err)
	}
	if list[1].Size != len(a.Data) || list[1].Hash != a.Hash {
		t.Errorf("meta = %+v", list[1])
	}
	got, err := s.Data(ctx, a.Hash)
	if err != nil || string(got) != string(a.Data) {
		t.Fatalf("Data = %q err %v", got, err)
	}
	if _, err := s.Data(ctx, strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Data unknown = %v", err)
	}
	fams, err := s.Families(ctx)
	if err != nil || len(fams) != 2 || fams[0] != "Alpha" || fams[1] != "Brand Sans" {
		t.Errorf("Families = %v err %v", fams, err)
	}
	n, err := s.DeleteFamily(ctx, "brand sans")
	if err != nil || n != 2 {
		t.Errorf("DeleteFamily = %d %v", n, err)
	}
}

func TestInsertEnforcesCap(t *testing.T) {
	s := scratchStore(t)
	ctx := context.Background()
	var batch []NewFont
	for i := 0; i < MaxFonts; i++ {
		batch = append(batch, newFont("Many", 400, string(rune('a'+i%26))+strings.Repeat("x", i)))
	}
	if err := s.Insert(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, []NewFont{newFont("One More", 400, "z")}); !errors.Is(err, ErrTooMany) {
		t.Errorf("61st = %v, want ErrTooMany", err)
	}
}

// A list load already running when a font is written must not be handed to
// the callers that list afterwards.
func TestWritesDetachRunningListLoad(t *testing.T) {
	s := scratchStore(t)
	ctx := context.Background()
	read, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.list = func(ctx context.Context) ([]Font, error) {
		out, err := s.query(ctx) // reads the table as it is now
		if calls.Add(1) == 1 {
			close(read)
			<-release
		}
		return out, err
	}
	stale := make(chan []Font, 1)
	go func() { out, _ := s.List(ctx); stale <- out }()
	<-read

	if err := s.Insert(ctx, []NewFont{newFont("Brand Sans", 400, "a")}); err != nil {
		t.Fatal(err)
	}
	// Joining the running load would block on release: fail instead of hanging.
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := s.List(lctx)
	if err != nil || len(out) != 1 {
		t.Fatalf("list after insert = %v, %v; want the new face", out, err)
	}
	if _, err := s.DeleteFamily(ctx, "Brand Sans"); err != nil {
		t.Fatal(err)
	}
	if out, err = s.List(lctx); err != nil || len(out) != 0 {
		t.Fatalf("list after delete = %v, %v; want none", out, err)
	}
	close(release)
	<-stale
}
