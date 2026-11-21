//go:build integration

package admin

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/frameticket"
)

func openTicketDB(t *testing.T, dsn string) *database.DB {
	t.Helper()
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestFrameTicketMintedOnOneServerOpensOnAnother_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs Postgres")
	}
	dbA, dbB := openTicketDB(t, dsn), openTicketDB(t, dsn) // two pools stand for two servers
	a := newRig(t, func(d *Deps) { d.FrameTickets = frameticket.NewStore(dbA) })
	b := newRig(t, func(d *Deps) { d.FrameTickets = frameticket.NewStore(dbB) })
	b.a.deps.Content.Preview = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("preview")) })
	a.a.deps.Content.Preview = b.a.deps.Content.Preview
	owner := b.sessions.byToken["owner-token"].ID // the session store is shared in production
	id := uuid.New()
	t.Cleanup(func() {
		_, _ = dbA.Pool().Exec(context.Background(), `DELETE FROM hatamoto_frame_tickets WHERE target = $1`, id.String())
	})
	tok := a.a.tickets.mint(ticketPreview, owner, id.String(), []byte("{}"))
	if tok == "" {
		t.Fatal("mint refused")
	}
	frame := map[string]string{"Sec-Fetch-Dest": "iframe"}
	path := "/admin/entities/" + id.String() + "/edit/preview/" + tok
	if rec := b.do("GET", path, "owner-token", nil, frame); rec.Code != 200 || rec.Body.String() != "preview" {
		t.Fatalf("minted on A, opened on B: %d %q", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", path, "owner-token", nil, frame); rec.Code != 404 {
		t.Fatalf("reused on B: %d", rec.Code)
	}
	if rec := a.do("GET", path, "owner-token", nil, frame); rec.Code != 404 {
		t.Fatalf("reused on A: %d", rec.Code)
	}
}

func TestChangesetTicketMintedOnOneServerOpensOnAnother_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs Postgres")
	}
	dbA, dbB := openTicketDB(t, dsn), openTicketDB(t, dsn)
	a := newRig(t, func(d *Deps) { d.FrameTickets = frameticket.NewStore(dbA) })
	b := newRig(t, func(d *Deps) { d.FrameTickets = frameticket.NewStore(dbB) })
	owner := a.sessions.byToken["owner-token"].ID
	target := changesetTarget(time.Now().UnixNano(), 0)
	t.Cleanup(func() {
		_, _ = dbA.Pool().Exec(context.Background(), `DELETE FROM hatamoto_frame_tickets WHERE target = $1`, target)
	})
	tok := a.a.tickets.mint(ticketChangesetPreview, owner, target, []byte("https://admin.example.test"))
	sess, payload, ok := b.a.tickets.consumeBound(ticketChangesetPreview, tok, target)
	if !ok || sess != owner || string(payload) != "https://admin.example.test" {
		t.Fatalf("consumeBound on B: ok=%v sess=%v payload=%q", ok, sess, payload)
	}
	if _, _, ok := a.a.tickets.consumeBound(ticketChangesetPreview, tok, target); ok {
		t.Error("reused on A")
	}
}
