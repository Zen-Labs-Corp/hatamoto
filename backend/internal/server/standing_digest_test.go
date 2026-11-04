package server

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/standing"
)

type digestRig struct {
	job     *digestJob
	sent    []string
	data    []email.RunDigestData
	claims  int
	claimed bool
}

func newDigestRig(configured bool, rows []standing.DigestRow) *digestRig {
	r := &digestRig{}
	r.job = &digestJob{
		configured: func() bool { return configured },
		rows:       func(context.Context, time.Time) ([]standing.DigestRow, error) { return rows, nil },
		claim: func(context.Context, time.Time) (bool, error) {
			r.claims++
			won := !r.claimed
			r.claimed = true
			return won, nil
		},
		owners: func(context.Context) ([]string, error) { return []string{"a@example.test", "b@example.test"}, nil },
		site:   func(context.Context) (string, string, string) { return "Site", "https://example.test/", "/panel" },
		send: func(_ context.Context, to string, d email.RunDigestData) error {
			r.sent = append(r.sent, to)
			r.data = append(r.data, d)
			return nil
		},
		now: func() time.Time { return time.Date(2030, 1, 2, 15, 0, 0, 0, time.UTC) },
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return r
}

var oneDigestRow = []standing.DigestRow{{Plugin: "pricer", Run: "prices", ChangesetID: 9, Changes: 3}}

func TestDigestJobMailsOwnersOncePerDay(t *testing.T) {
	r := newDigestRig(true, oneDigestRow)
	for range 2 { // two replicas, or a restart, on the same day
		if err := r.job.run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.sent) != 2 || r.sent[0] != "a@example.test" || r.sent[1] != "b@example.test" {
		t.Fatalf("sent to %v, want both owners once", r.sent)
	}
	d := r.data[0]
	if d.HistoryURL != "https://example.test/panel/settings/changes" || len(d.Runs) != 1 ||
		d.Runs[0].URL != "https://example.test/panel/plugins/pricer/changesets/9" || d.Runs[0].Changes != 3 {
		t.Fatalf("digest data = %+v", d)
	}
}

func TestDigestJobStaysQuietWithoutMailOrRuns(t *testing.T) {
	for name, r := range map[string]*digestRig{
		"mail not configured": newDigestRig(false, oneDigestRow),
		"nothing published":   newDigestRig(true, nil),
	} {
		if err := r.job.run(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(r.sent) != 0 || r.claims != 0 {
			t.Fatalf("%s: sent %v, claims %d; want neither (the day must stay unclaimed)", name, r.sent, r.claims)
		}
	}
}

func TestDigestJobUsesThePathAloneWithoutABaseURL(t *testing.T) {
	r := newDigestRig(true, oneDigestRow)
	r.job.site = func(context.Context) (string, string, string) { return "Site", "", "/panel" }
	if err := r.job.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.data[0].HistoryURL; !strings.HasPrefix(got, "/panel/") {
		t.Fatalf("history url = %q, want the path alone", got)
	}
}
