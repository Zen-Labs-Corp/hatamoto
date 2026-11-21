package admin

import (
	"net/url"
	"testing"
)

const termsAuditBase = "/admin/settings/taxonomies/terms"

// termsAuditOne checks that the request wrote exactly one safe row and returns it.
func termsAuditOne(t *testing.T, read func() []AuditEvent, action, target string) AuditEvent {
	t.Helper()
	evs := read()
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want exactly one %s", evs, action)
	}
	assertAuditSafe(t, evs)
	if ev := evs[0]; ev.Action != action || ev.Target != target || ev.Actor != "owner@example.com" {
		t.Fatalf("row = %+v, want %s on %s by the owner", ev, action, target)
	}
	return evs[0]
}

func TestTermsAuditCreateSaveMergeDelete(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	read := c.record()

	// A refused create (slug taken) writes no row.
	if rec := c.post(termsAuditBase, url.Values{"kind": {"category"}, "name": {"News"}}); rec.Code != 422 {
		t.Fatalf("duplicate create: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed create audited: %+v", evs)
	}

	if rec := c.post(termsAuditBase, url.Values{"kind": {"tag"}, "name": {"Rust"}}); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	evs := read()
	if len(evs) != 1 {
		t.Fatalf("create events = %+v", evs)
	}
	created := c.terms.terms[len(c.terms.terms)-1]
	ev := termsAuditOne(t, read, AuditTermCreated, created.ID.String())
	if ev.Detail["name"] != "Rust" || ev.Detail["slug"] != "rust" || ev.Detail["kind"] != "tag" {
		t.Fatalf("detail = %v", ev.Detail)
	}

	// Save: the edit form of the "Go" tag (id 33).
	c.post(termsAuditBase+"/"+fid(33).String(), url.Values{"name": {"Golang"}, "slug": {"go"}})
	evs = read()
	if len(evs) != 2 || evs[1].Action != AuditTermUpdated || evs[1].Target != fid(33).String() || evs[1].Detail["name"] != "Golang" || evs[1].Detail["kind"] != "tag" {
		t.Fatalf("save events = %+v", evs)
	}
	// A slug clash writes nothing.
	c.post(termsAuditBase+"/"+fid(33).String(), url.Values{"name": {"Golang"}, "slug": {"rust"}})
	if got := read(); len(got) != 2 {
		t.Fatalf("failed save audited: %+v", got)
	}

	// Merge and delete.
	if rec := c.post(termsAuditBase+"/"+fid(33).String()+"/merge", url.Values{"into": {created.ID.String()}}); rec.Code != 303 {
		t.Fatalf("merge: %d", rec.Code)
	}
	evs = read()
	if len(evs) != 3 || evs[2].Action != AuditTermMerged || evs[2].Target != fid(33).String() || evs[2].Detail["into"] != created.ID.String() {
		t.Fatalf("merge events = %+v", evs)
	}
	c.post(termsAuditBase+"/"+fid(31).String()+"/merge", url.Values{"into": {"nope"}})
	if got := read(); len(got) != 3 {
		t.Fatalf("failed merge audited: %+v", got)
	}
	if rec := c.post(termsAuditBase+"/"+created.ID.String()+"/delete", nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	evs = read()
	if len(evs) != 4 || evs[3].Action != AuditTermDeleted || evs[3].Target != created.ID.String() || evs[3].Detail["name"] != "Rust" {
		t.Fatalf("delete events = %+v", evs)
	}
	assertAuditSafe(t, evs)
}

func TestTermsAuditLocaleCopyLinkUnlink(t *testing.T) {
	t.Parallel()

	c, tg, id := termLocalesRig(t, "en", "pt-BR")
	read := c.record()
	path := termsAuditBase + "/" + id

	// A locale the site does not have is refused without a row.
	c.post(path+"/locale", url.Values{"locale": {"xx"}})
	if evs := read(); len(evs) != 0 {
		t.Fatalf("refused locale audited: %+v", evs)
	}
	c.post(path+"/locale", url.Values{"locale": {"en"}})
	ev := termsAuditOne(t, read, AuditTermUpdated, id)
	if ev.Detail["locale"] != "en" || len(ev.Detail) != 1 {
		t.Fatalf("locale detail = %v", ev.Detail)
	}

	c.post(path+"/copy", url.Values{"locale": {"pt-BR"}, "name": {"Tag"}, "slug": {"tag-pt"}})
	evs := read()
	if len(evs) != 2 || evs[1].Action != AuditTermCopied || evs[1].Detail["locale"] != "pt-BR" || evs[1].Target == id || evs[1].Target == "" {
		t.Fatalf("copy events = %+v", evs)
	}

	other := c.terms.terms[1].ID.String()
	c.post(path+"/link", url.Values{"term": {other}})
	evs = read()
	if len(evs) != 3 || evs[2].Action != AuditTermLinked || evs[2].Target != id {
		t.Fatalf("link events = %+v", evs)
	}
	c.post(path+"/link", url.Values{"term": {"nope"}})
	if got := read(); len(got) != 3 {
		t.Fatalf("failed link audited: %+v", got)
	}

	c.post(path+"/unlink", url.Values{"term": {id}})
	evs = read()
	if len(evs) != 4 || evs[3].Action != AuditTermUnlinked || evs[3].Target != id || len(tg.unlinked) != 1 {
		t.Fatalf("unlink events = %+v", evs)
	}
	assertAuditSafe(t, evs)
}
