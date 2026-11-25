//go:build integration

package standing

import (
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

func TestDigestListsOnlyTheLastDayOfAutomaticRuns(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	now := time.Now().UTC()
	recent, recentCS := insertRun(t, s, p, "cron", "published", true, now.Add(-2*time.Hour))
	insertRun(t, s, p, "cron", "published", true, now.Add(-30*time.Hour))
	insertRun(t, s, p, "owner", "published", false, now.Add(-time.Hour))
	insertRun(t, s, p, "cron", "staged", false, now.Add(-time.Hour))
	got, err := s.Digest(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var mine []DigestRow
	for _, d := range got {
		if d.Plugin == p {
			mine = append(mine, d)
		}
	}
	if len(mine) != 1 || mine[0].RunID != recent || mine[0].ChangesetID != recentCS || mine[0].Run != "prices" {
		t.Fatalf("Digest = %+v, want only the recent automatic run", mine)
	}
}

func TestClaimDigestHasOneWinnerPerPeriod(t *testing.T) {
	s, ctx := newStore(t)
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_digest_state WHERE name = $1`, DigestName) })
	s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_digest_state WHERE name = $1`, DigestName)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	first, err := s.ClaimDigest(ctx, day)
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v; want true", first, err)
	}
	if again, err := s.ClaimDigest(ctx, day); err != nil || again {
		t.Fatalf("second claim of the same period = %v, %v; want false", again, err)
	}
	if next, err := s.ClaimDigest(ctx, day.Add(24*time.Hour)); err != nil || !next {
		t.Fatalf("claim of the next period = %v, %v; want true", next, err)
	}
}

func TestDigestSplitsAutomaticCreatesFromUpdates(t *testing.T) {
	s, ctx := newStore(t)
	p := testPlugin(t, s, ctx)
	_, cs := insertRun(t, s, p, "cron", "published", true, time.Now().UTC().Add(-time.Hour))
	items := []struct {
		kind, op, result string
		before           any
		token            any
	}{
		{"entity", "upsert", "applied", nil, nil},              // create
		{"entity", "upsert", "applied", nil, nil},              // create
		{"entity", "upsert", "applied", `{"name":"x"}`, "tok"}, // update
		{"term", "upsert", "applied", nil, nil},                // not an entity create by kind
		{"entity", "upsert", "skipped", `{"name":"y"}`, "tok"}, // not applied
	}
	for i, it := range items {
		if _, err := s.DB.Pool().Exec(ctx, `
			INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, result, before, before_token)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)`,
			cs, i, it.kind, it.op, "k"+string(rune('a'+i)), it.result, it.before, it.token); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Digest(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Plugin == p {
			if d.Created != 2 || d.Changes != 2 {
				t.Fatalf("Created=%d Changes=%d, want 2 and 2", d.Created, d.Changes)
			}
			return
		}
	}
	t.Fatal("run missing from the digest")
}

// Hosts come from the links the approval's link fields hold in applied creates,
// once per entry and host; an approval without link fields yields none.
func TestDigestCountsLinkedHostsOfAutomaticCreates(t *testing.T) {
	s, ctx := newStore(t)
	for _, tc := range []struct {
		name string
		link []string
		want map[string]int
	}{
		{"declared", []string{"ticket_url", "online_url"}, map[string]int{"tickets.example": 2, "meet.example": 1}},
		{"undeclared", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testPlugin(t, s, ctx)
			policy := Policy{Schemas: []string{"event"}, MaxPerHour: 5, Create: &CreatePolicy{Schemas: []string{"event"}, MaxPerHour: 5, LinkFields: tc.link}}
			ap, err := s.Approve(ctx, p, "prices", policy, sampleFP("1.0.0"), "owner@example.com")
			if err != nil {
				t.Fatal(err)
			}
			_, cs := insertRun(t, s, p, "cron", "published", true, time.Now().UTC().Add(-time.Hour))
			if _, err := s.DB.Pool().Exec(ctx, `UPDATE hatamoto_changesets SET approval_id = $1 WHERE id = $2`, ap.ID, cs); err != nil {
				t.Fatal(err)
			}
			for i, attrs := range []map[string]any{
				{"ticket_url": "https://tickets.example/e/1", "online_url": "https://tickets.example/live"},
				{"ticket_url": "https://tickets.example/e/2", "online_url": "https://meet.example/r"},
				{"ticket_url": "http://bad.example/e/3", "notes": "https://other.example/"},
			} {
				payload, _ := jsonx.Marshal(map[string]any{"schema": "event", "attributes": attrs})
				if _, err := s.DB.Pool().Exec(ctx, `
					INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, source_key, result, payload)
					VALUES ($1, $2, 'entity', 'upsert', $3, 'applied', $4::jsonb)`, cs, i, "k"+string(rune('a'+i)), string(payload)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Digest(ctx, time.Now().UTC().Add(-24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range got {
				if d.Plugin != p {
					continue
				}
				if d.Created != 3 || len(d.Hosts) != len(tc.want) {
					t.Fatalf("Created=%d Hosts=%v, want 3 and %v", d.Created, d.Hosts, tc.want)
				}
				for h, n := range tc.want {
					if d.Hosts[h] != n {
						t.Fatalf("Hosts = %v, want %v", d.Hosts, tc.want)
					}
				}
				return
			}
			t.Fatal("run missing from the digest")
		})
	}
}
