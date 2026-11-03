package admin

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

type fakeRunStarter struct {
	err      error
	plugin   string
	run      string
	in       StartRunInput
	started  int
	lastUUID uuid.UUID
}

func (f *fakeRunStarter) StartRun(_ context.Context, plugin, run string, in StartRunInput) (uuid.UUID, error) {
	if f.err != nil {
		return uuid.Nil, f.err
	}
	f.started++
	f.plugin, f.run, f.in = plugin, run, in
	f.lastUUID = uuid.New()
	return f.lastUUID, nil
}

func newRunRig(t *testing.T) (*rig, *fakeRunStarter) {
	t.Helper()
	fr := &fakeRunStarter{}
	return newRig(t, func(d *Deps) { d.Growth.PluginRuns = fr }), fr
}

const runStartPath = "/admin/plugins/hello/runs/import/start"

func TestPluginRunStart(t *testing.T) {
	t.Parallel()

	g, fr := newRunRig(t)
	up := uuid.New()
	rec := g.do("POST", runStartPath, "owner-token", url.Values{
		"input_site": {"https://example.com"}, "hosts": {"example.com", "cdn.example.com"}, "upload_id": {up.String()}, "stray": {"x"},
	}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/plugins/hello/settings" {
		t.Fatalf("start: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if fr.started != 1 || fr.plugin != "hello" || fr.run != "import" || fr.in.Inputs["site"] != "https://example.com" ||
		len(fr.in.Inputs) != 1 || len(fr.in.Hosts) != 2 || fr.in.UploadID == nil || *fr.in.UploadID != up {
		t.Fatalf("starter got %+v (%s/%s)", fr.in, fr.plugin, fr.run)
	}
	if rec := g.do("POST", runStartPath, "owner-token", url.Values{"upload_id": {"not-a-uuid"}}, nil); rec.Code != 400 || fr.started != 1 {
		t.Errorf("bad upload id: %d", rec.Code)
	}
}

func TestPluginRunStartErrors(t *testing.T) {
	t.Parallel()

	g, fr := newRunRig(t)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{ErrRunActive, 409},
		{ErrRunNotFound, 404},
		{&RunInputError{Msg: "Site is required"}, 400},
		{ErrRunsUnavailable, 503},
		{errors.New("db down"), 500},
	} {
		fr.err = tc.err
		if rec := g.do("POST", runStartPath, "owner-token", url.Values{}, nil); rec.Code != tc.want {
			t.Errorf("%v: %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
	g2 := newRig(t, func(*Deps) {})
	if rec := g2.do("POST", runStartPath, "owner-token", url.Values{}, nil); rec.Code != 503 {
		t.Errorf("not wired: %d", rec.Code)
	}
}

func TestPluginRunStartOwnerOnlyAndNeedsCSRF(t *testing.T) {
	t.Parallel()

	g, fr := newRunRig(t)
	g.realTokens()
	if rec := g.do("POST", runStartPath, "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
	if rec := g.do("POST", runStartPath, "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("no CSRF: %d, want 403", rec.Code)
	}
	if rec := g.do("GET", runStartPath, "owner-token", nil, nil); rec.Code == 200 || rec.Code == 303 {
		t.Errorf("GET must not start a run: %d", rec.Code)
	}
	if rec := g.do("POST", runStartPath, "", url.Values{}, nil); rec.Code == 303 && rec.Header().Get("Location") == "/admin/plugins/hello/settings" {
		t.Errorf("anonymous start: %d", rec.Code)
	}
	if fr.started != 0 {
		t.Fatalf("%d runs started by refused requests", fr.started)
	}
}
