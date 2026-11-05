package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/media"
)

type fakeMediaFormats struct {
	set     media.Settings
	state   media.State
	started []string
	err     error
}

func (f *fakeMediaFormats) Settings(context.Context) (media.Settings, error) { return f.set, nil }
func (f *fakeMediaFormats) SaveSettings(_ context.Context, s media.Settings) error {
	f.set = s
	return nil
}
func (f *fakeMediaFormats) Status(context.Context) (media.State, error) { return f.state, nil }
func (f *fakeMediaFormats) StartRebuild(_ context.Context, actor string) error {
	f.started = append(f.started, actor)
	return f.err
}

func TestMediaFormatsCard(t *testing.T) {
	g := newContentRig(t)
	fake := &fakeMediaFormats{set: media.Settings{WebP: true}, state: media.State{Running: true, Total: 40, Done: 10}}
	g.a.deps.MediaFormats = fake
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }

	rec := g.do("GET", "/admin/media", "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{"Image formats", "WebP copies", "AVIF copies", "10 of 40 images", `action="/admin/media/rebuild"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	if strings.Contains(body, "disabled") {
		t.Error("rebuild button is disabled while a run is active")
	}

	fake.state = media.State{Last: &media.LastRun{FinishedAt: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC), Images: 12, Skipped: 2}}
	body = g.do("GET", "/admin/media", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Last rebuild:") || !strings.Contains(body, "2 skipped") {
		t.Error("last run line missing")
	}
	if strings.Contains(body, "disabled") {
		t.Error("rebuild button is disabled while idle")
	}

	g.do("POST", "/admin/media/formats", "owner-token", url.Values{"webp": {"off"}, "avif": {"on"}}, nil)
	if fake.set.WebP || !fake.set.AVIF || len(events) != 1 || events[0].Action != "media.formats.settings" {
		t.Errorf("save: %+v %v", fake.set, events)
	}
	g.do("POST", "/admin/media/rebuild", "owner-token", url.Values{}, nil)
	if len(fake.started) != 1 {
		t.Errorf("rebuild starts: %v", fake.started)
	}

	body = g.do("GET", "/admin/media", "editor-token", nil, nil).Body.String()
	if strings.Contains(body, "Image formats") {
		t.Error("non-owner sees the card")
	}
	if rec := g.do("POST", "/admin/media/rebuild", "editor-token", url.Values{}, nil); rec.Code != 403 || len(fake.started) != 1 {
		t.Errorf("non-owner rebuild: %d %v", rec.Code, fake.started)
	}
}
