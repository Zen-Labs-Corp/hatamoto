package admin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/previewlink"
)

// fakeLinks keeps preview links in memory. It hands out a fixed token.
type fakeLinks struct {
	mu    sync.Mutex
	links []previewlink.Link
	max   int
	ttls  []time.Duration
}

func (f *fakeLinks) Create(_ context.Context, id uuid.UUID, _ *uuid.UUID, name string, ttl time.Duration) (string, *previewlink.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.max > 0 && len(f.links) >= f.max {
		return "", nil, previewlink.ErrTooMany
	}
	f.ttls = append(f.ttls, ttl)
	l := previewlink.Link{ID: fid(60 + len(f.links)), EntityID: id, Tail: "x7Kq", CreatedByName: name, ExpiresAt: t0.Add(ttl)}
	f.links = append(f.links, l)
	return "SECRETTOKEN0123456789abcdefghijklmnopqrstuvx7Kq", &l, nil
}

func (f *fakeLinks) List(context.Context, uuid.UUID) ([]previewlink.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]previewlink.Link(nil), f.links...), nil
}

func (f *fakeLinks) Revoke(_ context.Context, _, link uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, l := range f.links {
		if l.ID == link {
			f.links = append(f.links[:i], f.links[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func TestSharePreviewFlow(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	fl := &fakeLinks{}
	c.rig.a.deps.Content.PreviewLinks = fl
	var audits []AuditEvent
	c.rig.a.deps.Audit = func(_ context.Context, e AuditEvent) { audits = append(audits, e) }
	draft := "/admin/entities/" + fid(12).String()

	// A draft offers the page; the empty state is a golden.
	has(t, c.get(t, "owner-token", draft, ""), "Share preview")
	body := c.get(t, "owner-token", draft+"/share", "entity-share-empty")
	has(t, body, "No active links", "How long the link works", "Create link")
	if strings.Contains(body, "SECRETTOKEN") {
		t.Error("token on a fresh page")
	}

	// Creating shows the whole link once.
	rec := c.post(draft+"/share", url.Values{"lifetime": {"7d"}})
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	has(t, rec.Body.String(), "/preview/SECRETTOKEN0123456789abcdefghijklmnopqrstuvx7Kq", "Copy this link now", "shown once", "readonly")
	if len(fl.ttls) != 1 || fl.ttls[0] != 7*24*time.Hour {
		t.Errorf("ttl = %v", fl.ttls)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("share page is cacheable")
	}
	// A later view shows only the tail.
	later := c.get(t, "owner-token", draft+"/share", "entity-share-links")
	if strings.Contains(later, "SECRETTOKEN") || !strings.Contains(later, "…x7Kq") {
		t.Error("the list leaks or lacks the tail")
	}
	if len(audits) != 1 || audits[0].Action != auditPreviewLinkCreate {
		t.Fatalf("audits = %+v", audits)
	}
	if b, _ := jsonx.Marshal(audits[0].Detail); strings.Contains(string(b), "SECRETTOKEN") {
		t.Error("token in audit")
	}

	// Bad lifetimes are refused, nothing is made.
	for _, v := range []string{"", "999d", "0", "-1h"} {
		if rec := c.post(draft+"/share", url.Values{"lifetime": {v}}); rec.Code != 422 || len(fl.ttls) != 1 {
			t.Errorf("lifetime %q: %d, %d made", v, rec.Code, len(fl.ttls))
		}
	}

	// The cap is a clear error.
	fl.max = 1
	rec = c.post(draft+"/share", url.Values{"lifetime": {"1h"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "10 active links") {
		t.Errorf("cap: %d", rec.Code)
	}

	// Revoking removes the link and audits it.
	revoke := draft + "/share/" + fl.links[0].ID.String() + "/revoke"
	if rec := c.post(revoke, url.Values{}); rec.Code != 303 {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if len(fl.links) != 0 || len(audits) != 2 || audits[1].Action != auditPreviewLinkRevoke {
		t.Errorf("links=%d audits=%+v", len(fl.links), audits)
	}
}

func TestSharePreviewRefusesPublishedAndMissingStore(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	pub := "/admin/entities/" + fid(11).String()
	if rec := c.do("GET", pub+"/share", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("no store: %d", rec.Code)
	}
	if strings.Contains(c.get(t, "owner-token", pub, ""), "Share preview") {
		t.Error("share link without a store")
	}
	c.rig.a.deps.Content.PreviewLinks = &fakeLinks{}
	if strings.Contains(c.get(t, "owner-token", pub, ""), "Share preview") {
		t.Error("share link on a published entity")
	}
	body := c.get(t, "owner-token", pub+"/share", "")
	has(t, body, "already published")
	if strings.Contains(body, "Create link") {
		t.Error("create form for a published entity")
	}
	if rec := c.post(pub+"/share", url.Values{"lifetime": {"1h"}}); rec.Code != 409 && rec.Code != 422 {
		t.Errorf("create on published: %d", rec.Code)
	}
}
