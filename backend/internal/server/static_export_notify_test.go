package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/email"
)

type exportFailRig struct {
	n         *exportFailNotifier
	sent      []string
	data      []email.StaticExportFailedData
	prevAsked []uuid.UUID
	prev      bool
	sendErr   map[string]error
}

func newExportFailRig(configured bool) *exportFailRig {
	r := &exportFailRig{}
	r.n = &exportFailNotifier{
		configured: func() bool { return configured },
		owners:     func(context.Context) ([]string, error) { return []string{"a@example.test", "b@example.test"}, nil },
		site:       func(context.Context) (string, string, string) { return "Site", "https://example.test/", "/panel" },
		location:   func(context.Context) *time.Location { return time.UTC },
		previousFailed: func(_ context.Context, cur uuid.UUID) (bool, error) {
			r.prevAsked = append(r.prevAsked, cur)
			return r.prev, nil
		},
		send: func(_ context.Context, to string, d email.StaticExportFailedData) error {
			r.sent = append(r.sent, to)
			r.data = append(r.data, d)
			return r.sendErr[to]
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return r
}

var exportFailAt = time.Date(2030, 1, 2, 15, 4, 0, 0, time.UTC)

func (r *exportFailRig) finish(ok bool) {
	r.n.RunFinished(context.Background(), uuid.New(), ok, exportFailAt, "upload refused")
}

func TestExportFailStreak(t *testing.T) {
	t.Parallel()

	r := newExportFailRig(true)
	// none -> fail: mails both owners.
	r.finish(false)
	if len(r.sent) != 2 || r.sent[0] != "a@example.test" || r.sent[1] != "b@example.test" {
		t.Fatalf("none->fail sent to %v", r.sent)
	}
	d := r.data[0]
	if d.SiteName != "Site" || d.When != "2 Jan 2030 15:04" || d.Error != "upload refused" || d.HostingURL != "https://example.test/panel/settings/hosting" {
		t.Errorf("data = %+v", d)
	}
	// fail -> fail: the streak was mailed.
	r.prev, r.sent = true, nil
	r.finish(false)
	if len(r.sent) != 0 {
		t.Errorf("fail->fail sent to %v", r.sent)
	}
	// fail -> ok -> fail: a success clears the streak, so the next failure mails again.
	r.finish(true)
	r.prev = false // the finished run before the new failure is the success
	r.finish(false)
	if len(r.sent) != 2 {
		t.Errorf("fail->ok->fail sent to %v, want a second mail to both owners", r.sent)
	}
}

func TestExportFailQuietCases(t *testing.T) {
	t.Parallel()

	t.Run("mail not configured", func(t *testing.T) {
		r := newExportFailRig(false)
		r.finish(false)
		if len(r.sent) != 0 || len(r.prevAsked) != 0 {
			t.Errorf("sent %v, asked %d times", r.sent, len(r.prevAsked))
		}
	})
	t.Run("no run row", func(t *testing.T) {
		r := newExportFailRig(true)
		r.n.RunFinished(context.Background(), uuid.Nil, false, exportFailAt, "busy")
		if len(r.sent) != 0 || len(r.prevAsked) != 0 {
			t.Errorf("sent %v, asked %d times", r.sent, len(r.prevAsked))
		}
	})
	t.Run("ok run", func(t *testing.T) {
		r := newExportFailRig(true)
		r.finish(true)
		if len(r.sent) != 0 {
			t.Errorf("sent %v", r.sent)
		}
	})
	t.Run("no site address", func(t *testing.T) {
		r := newExportFailRig(true)
		r.n.site = func(context.Context) (string, string, string) { return "Site", " ", "/panel" }
		r.finish(false)
		if len(r.sent) != 0 {
			t.Errorf("sent %v", r.sent)
		}
	})
	t.Run("previous run unreadable", func(t *testing.T) {
		r := newExportFailRig(true)
		r.n.previousFailed = func(context.Context, uuid.UUID) (bool, error) { return false, errors.New("db down") }
		r.finish(false)
		if len(r.sent) != 0 {
			t.Errorf("sent %v", r.sent)
		}
	})
	t.Run("nil notifier", func(t *testing.T) {
		var n *exportFailNotifier
		n.RunFinished(context.Background(), uuid.New(), false, exportFailAt, "x")
	})
}

func TestExportFailPassesTheCurrentRun(t *testing.T) {
	t.Parallel()

	r := newExportFailRig(true)
	id := uuid.New()
	r.n.RunFinished(context.Background(), id, false, exportFailAt, "x")
	if len(r.prevAsked) != 1 || r.prevAsked[0] != id {
		t.Errorf("previousFailed asked about %v, want %v", r.prevAsked, id)
	}
}

func TestExportFailOneSendErrorKeepsGoing(t *testing.T) {
	t.Parallel()

	r := newExportFailRig(true)
	r.sendErr = map[string]error{"a@example.test": errors.New("smtp down")}
	r.finish(false)
	if len(r.sent) != 2 {
		t.Errorf("sent to %v, want both owners tried", r.sent)
	}
}

func TestExportFailClipsTheError(t *testing.T) {
	t.Parallel()

	r := newExportFailRig(true)
	r.n.RunFinished(context.Background(), uuid.New(), false, exportFailAt, strings.Repeat("e", 500))
	if got := []rune(r.data[0].Error); len(got) > maxExportMailError+1 {
		t.Errorf("error is %d runes", len(got))
	}
}
