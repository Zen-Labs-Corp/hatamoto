//go:build integration

package backup

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// With the engine's pool behind a transaction pooler, the backup still works:
// its queries use the pool and pg_dump uses the direct URL.
func TestPooler_EngineBacksUpThroughPooledPool(t *testing.T) {
	ctx := context.Background()
	direct, appURL := scratchDB(t, modeCreateRole)
	needMatchingPgDump(t, direct)

	pooled, err := pgxpool.New(ctx, pgbouncertest.Start(t, appURL, 2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pooled.Close)

	data := t.TempDir()
	e := New(Options{Data: data, MediaDir: t.TempDir(), DatabaseURL: appURL, Pool: pooled, Version: "test"})
	info, err := e.Create(ctx, Request{Kind: KindManual})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.State != StateOK {
		t.Fatalf("state = %s (%s)", info.State, info.Error)
	}

	f, err := os.Open(filepath.Join(data, "backups", info.ID+".tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var core bool
	for tr := tar.NewReader(f); ; {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		core = core || h.Name == "core.dump"
	}
	if !core {
		t.Fatal("archive lacks core.dump")
	}
}
