package admin

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
)

var jsonAccept = map[string]string{"Accept": "application/json"}

type runStartReply struct {
	OK    bool   `json:"ok"`
	RunID string `json:"run_id"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

func TestPageRunStartJSON(t *testing.T) {
	t.Parallel()

	g, _, _, fs := newRunsRig(t)
	rec := g.do("POST", runStartPath, "owner-token", url.Values{
		"input_site": {"https://example.com"}, "hosts": {"evil.example.com"}, "upload_id": {uuid.NewString()},
	}, jsonAccept)
	if rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var got runStartReply
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.RunID != fs.lastUUID.String() || got.URL != "/admin/plugins/hello/runs/"+got.RunID {
		t.Errorf("reply %+v", got)
	}
	if fs.started != 1 || len(fs.in.Inputs) != 1 || fs.in.Inputs["site"] != "https://example.com" {
		t.Errorf("starter inputs %+v", fs.in)
	}
	if len(fs.in.Hosts) != 1 || fs.in.Hosts[0] != "old.example.com" || fs.in.UploadID != nil {
		t.Errorf("hosts %v upload %v: a page never picks them", fs.in.Hosts, fs.in.UploadID)
	}
}

func TestPageRunStartUndeclaredRun(t *testing.T) {
	t.Parallel()

	g, _, _, fs := newRunsRig(t)
	rec := g.do("POST", "/admin/plugins/hello/runs/nope/start", "owner-token", url.Values{}, jsonAccept)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"ok":false`) || fs.started != 0 {
		t.Errorf("undeclared run: %d %s started=%d", rec.Code, rec.Body.String(), fs.started)
	}
}

func TestPageRunStartCaps(t *testing.T) {
	t.Parallel()

	g, _, _, fs := newRunsRig(t)
	many := url.Values{}
	for i := range 17 {
		many.Set(fmt.Sprintf("input_k%d", i), "v")
	}
	long := url.Values{"input_site": {strings.Repeat("a", 4097)}}
	for name, form := range map[string]url.Values{"17 inputs": many, "long value": long} {
		for _, hdr := range []map[string]string{jsonAccept, nil} {
			if rec := g.do("POST", runStartPath, "owner-token", form, hdr); rec.Code != 400 {
				t.Errorf("%s (json=%v): %d, want 400", name, hdr != nil, rec.Code)
			}
		}
	}
	if fs.started != 0 {
		t.Fatalf("%d runs started by refused inputs", fs.started)
	}
	ok := url.Values{"input_site": {strings.Repeat("a", 4096)}}
	if rec := g.do("POST", runStartPath, "owner-token", ok, jsonAccept); rec.Code != 200 {
		t.Errorf("4096 bytes: %d", rec.Code)
	}
}

func TestPageRunStartActive(t *testing.T) {
	t.Parallel()

	g, _, _, fs := newRunsRig(t)
	fs.err = ErrRunActive
	rec := g.do("POST", runStartPath, "owner-token", url.Values{}, jsonAccept)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already has a run in progress") {
		t.Errorf("active: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPageRunStartOwnerOnlyAndNeedsCSRF(t *testing.T) {
	t.Parallel()

	g, _, _, fs := newRunsRig(t)
	g.realTokens()
	if rec := g.do("POST", runStartPath, "editor-token", url.Values{}, jsonAccept); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
	hdr := map[string]string{"Accept": "application/json", "X-CSRF-Token": ""}
	if rec := g.do("POST", runStartPath, "owner-token", url.Values{}, hdr); rec.Code != 403 {
		t.Errorf("no CSRF: %d, want 403", rec.Code)
	}
	if fs.started != 0 {
		t.Fatalf("%d runs started by refused requests", fs.started)
	}
}
