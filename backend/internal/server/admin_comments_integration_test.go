//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

type nopMailer struct{}

func (nopMailer) SendCommentConfirmation(context.Context, string, email.CommentConfirmationData) error {
	return nil
}

// modeRecorder records only OnModeChanged; uuid.Nil is a type-default change.
type modeRecorder struct {
	comment.NopObserver
	entities []uuid.UUID
	types    int
}

func (m *modeRecorder) OnModeChanged(_ context.Context, id uuid.UUID) {
	if id == uuid.Nil {
		m.types++
		return
	}
	m.entities = append(m.entities, id)
}

// TestCommentsAdminIntegration drives the adapter against a migrated database.
// Comments and commenters are seeded through comment.Service only.
func TestCommentsAdminIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	secret := env.cfg.Secret
	if secret == "" {
		t.Fatal("no server secret in the test config")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := comment.New(env.db, secret, logger)
	entities := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), logger)
	a := commentsAdmin{svc: svc, db: env.db, entities: entities}
	modes := &modeRecorder{}
	svc.Register(modes)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	sel := func(dst any, q string, args ...any) {
		t.Helper()
		must(env.db.Pool().QueryRow(ctx, q, args...).Scan(dst))
	}

	// An actor (comment settings and replies reference a user), a custom and a built-in type, one entity.
	actor := uuid.New()
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_users (id, email, role, name) VALUES ($1, 'owner@cmm.test', 'owner', 'Owner')`, actor); err != nil {
		t.Fatal(err)
	}
	sch, err := entities.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Cmm Adm", Slug: "cmm-adm",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/cmm-adm/{slug}"}`),
	})
	must(err)
	var builtinID uuid.UUID
	sel(&builtinID, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, is_builtin) VALUES ('Cmm Builtin', 'cmm-builtin', '{"type":"object"}', true) RETURNING id`)
	ent, err := entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Cmm Entity", Slug: "cmm-entity", Attributes: jsontext.Value(`{}`)})
	must(err)

	// Settings: off -> on records the actor.
	was, err := a.SetEnabled(ctx, true, actor.String())
	must(err)
	if was {
		t.Error("SetEnabled(true) was = true on a fresh site")
	}
	var updatedBy *uuid.UUID
	sel(&updatedBy, `SELECT updated_by FROM hatamoto_comment_settings LIMIT 1`)
	if updatedBy == nil || *updatedBy != actor {
		t.Errorf("updated_by = %v, want %v", updatedBy, actor)
	}
	if was, err = a.SetEnabled(ctx, true, actor.String()); err != nil || !was {
		t.Errorf("second SetEnabled was = %v err = %v, want true", was, err)
	}
	if s, err := a.Settings(ctx); err != nil || !s.Enabled {
		t.Fatalf("Settings = %+v, %v", s, err)
	}
	bad, _ := a.Settings(ctx)
	bad.PageSize = 0
	var fe *admin.CommentFieldError
	if err := a.SaveSettings(ctx, bad, actor.String()); !errors.As(err, &fe) || fe.Field != "page_size" {
		t.Errorf("SaveSettings(page_size 0) = %v", err)
	}

	// Seed through the model: a draft, then Confirm.
	seed := func(mail, name, body string, confirm bool) uuid.UUID {
		t.Helper()
		res, err := commentfmt.Render(body)
		must(err)
		c, err := svc.Store(ctx, comment.Draft{
			EntityID: ent.ID, Email: mail, AuthorName: name, BodyRaw: body, BodyHTML: res.HTML,
			HasLinks: res.HasLinks, LinkDomains: res.LinkDomains, BodyHash: res.BodyHash,
			SpamVerdict: "allow", Status: comment.StatusDraft, Locale: "und",
		})
		must(err)
		if confirm {
			if _, err := svc.Confirm(ctx, c.ID); err != nil {
				t.Fatal(err)
			}
		}
		return c.ID
	}
	a1 := seed("a@cmm.test", "Alice", "see https://spam.example/x", true)
	a2 := seed("a@cmm.test", "Alice", "hello from a", true)
	b1 := seed("b@cmm.test", "Bob", "hello from b", true)
	b2 := seed("b@cmm.test", "Bob", "second from b", true)
	c1 := seed("c@cmm.test", "Cara", "hello from c", true)
	c2 := seed("c@cmm.test", "Cara", "second from c", true)
	ghost := seed("g@cmm.test", "Ghost", "never confirmed", false)

	// Counts and the queue.
	counts, err := a.Counts(ctx)
	must(err)
	sc, err := svc.StatusCounts(ctx)
	must(err)
	if counts.Pending != 6 || counts.Pending != sc[comment.StatusPending] || counts.Approved != 0 {
		t.Fatalf("Counts = %+v, StatusCounts = %v", counts, sc)
	}
	page, err := a.Queue(ctx, admin.CommentFilter{Status: "pending", Page: 1, PerPage: 50})
	must(err)
	if len(page.Rows) != 6 || page.HasMore {
		t.Fatalf("Queue pending: %d rows, HasMore %v", len(page.Rows), page.HasMore)
	}
	var link admin.CommentRow
	for _, r := range page.Rows {
		if r.ID == a1.String() {
			link = r
		}
		if r.EntityTitle != "Cmm Entity" {
			t.Errorf("EntityTitle = %q", r.EntityTitle)
		}
	}
	if !link.HasLinks || !slices.Contains(link.Reasons, comment.ReasonLink) {
		t.Errorf("link row = %+v, want reason %q", link, comment.ReasonLink)
	}
	for _, n := range []int{1, 2, 3} {
		p, err := a.Queue(ctx, admin.CommentFilter{Status: "pending", Page: n, PerPage: 2})
		must(err)
		if len(p.Rows) != 2 || p.HasMore != (n < 3) {
			t.Errorf("page %d: %d rows, HasMore %v", n, len(p.Rows), p.HasMore)
		}
	}
	if p, err := a.Queue(ctx, admin.CommentFilter{Status: "pending", HasLinks: true, Page: 1, PerPage: 50}); err != nil || len(p.Rows) != 1 {
		t.Errorf("HasLinks queue = %d rows, %v", len(p.Rows), err)
	}
	if p, err := a.Queue(ctx, admin.CommentFilter{Status: "pending", CommenterID: link.CommenterID, Page: 1, PerPage: 50}); err != nil || len(p.Rows) != 2 {
		t.Errorf("commenter queue = %d rows, %v, want Alice's 2", len(p.Rows), err)
	}
	if p, err := a.Queue(ctx, admin.CommentFilter{Query: "second", Page: 1, PerPage: 50}); err != nil || len(p.Rows) != 2 {
		t.Errorf("search queue = %d rows, %v", len(p.Rows), err)
	}
	one, err := a.Comment(ctx, a1.String())
	must(err)
	if !one.CreatedAt.Equal(link.CreatedAt) {
		t.Errorf("Comment CreatedAt %v != queue %v", one.CreatedAt, link.CreatedAt)
	}
	one.CreatedAt, link.CreatedAt = time.Time{}, time.Time{}
	if !slices.Equal(one.Reasons, link.Reasons) {
		t.Errorf("Comment reasons %v != queue %v", one.Reasons, link.Reasons)
	}
	one.Reasons, link.Reasons = nil, nil
	if one.EditedAt != nil || link.EditedAt != nil {
		t.Error("unexpected EditedAt")
	}
	if one.ID != link.ID || one.EntityID != link.EntityID || one.EntityTitle != link.EntityTitle || one.Status != link.Status ||
		one.BodyHTML != link.BodyHTML || one.BodyRaw != link.BodyRaw || one.AuthorName != link.AuthorName ||
		one.CommenterID != link.CommenterID || one.Trust != link.Trust || one.ApprovedCount != link.ApprovedCount ||
		one.HasLinks != link.HasLinks || one.Score != link.Score {
		t.Errorf("Comment = %+v, queue row = %+v", one, link)
	}
	if _, err := a.Comment(ctx, uuid.NewString()); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("unknown comment err = %v", err)
	}

	// Moderation.
	if n, err := a.Approve(ctx, []string{a2.String()}, actor.String()); err != nil || n != 1 {
		t.Fatalf("Approve = %d, %v", n, err)
	}
	row, err := a.Comment(ctx, a2.String())
	must(err)
	if row.Status != "approved" {
		t.Errorf("a2 status = %s", row.Status)
	}
	aliceID := row.CommenterID
	if tr, err := a.Vouch(ctx, aliceID, actor.String()); err != nil || tr != "known" {
		t.Errorf("Vouch new = %q, %v", tr, err)
	}
	if _, err := a.Vouch(ctx, uuid.NewString(), actor.String()); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("Vouch unknown err = %v", err)
	}

	// Bulk approve; a draft id is skipped and stays a draft.
	if n, err := a.Approve(ctx, []string{c1.String(), c2.String(), ghost.String()}, actor.String()); err != nil || n != 2 {
		t.Errorf("bulk Approve = %d, %v, want 2", n, err)
	}
	// Rows that cannot make the move are skipped, not fatal: c1 and c2 are approved already.
	if n, err := a.Approve(ctx, []string{c1.String(), c2.String()}, actor.String()); err != nil || n != 0 {
		t.Errorf("re-Approve = %d, %v, want 0 and no error", n, err)
	}
	if g, err := a.Comment(ctx, ghost.String()); err != nil || g.Status != "draft" {
		t.Errorf("ghost = %+v, %v", g, err)
	}

	// Reply and edit.
	raw := "Thanks <b>all</b>"
	replyID, err := a.Reply(ctx, a2.String(), raw, actor.String(), "Site Team")
	must(err)
	reply, err := a.Comment(ctx, replyID)
	must(err)
	if want, _ := commentfmt.Render(raw); reply.Status != "approved" || reply.AuthorUserID != actor.String() ||
		reply.ParentID != a2.String() || reply.BodyHTML != want.HTML || reply.CommenterID != "" || reply.AuthorName != "Site Team" {
		t.Errorf("reply = %+v", reply)
	}
	if _, err := a.Reply(ctx, a2.String(), "  ", actor.String(), "Site Team"); !errors.Is(err, admin.ErrCommentEmpty) {
		t.Errorf("empty reply err = %v", err)
	}
	if err := a.Edit(ctx, a2.String(), "edited *text*", actor.String()); err != nil {
		t.Fatal(err)
	}
	edited, err := a.Comment(ctx, a2.String())
	must(err)
	if want, _ := commentfmt.Render("edited *text*"); edited.BodyRaw != "edited *text*" || edited.BodyHTML != want.HTML {
		t.Errorf("edited = %+v", edited)
	}
	if err := a.Edit(ctx, a2.String(), "", actor.String()); !errors.Is(err, admin.ErrCommentEmpty) {
		t.Errorf("empty edit err = %v", err)
	}
	if err := a.Edit(ctx, a2.String(), strings.Repeat("x", commentfmt.MaxBodyBytes+1), actor.String()); !errors.Is(err, admin.ErrCommentTooLong) {
		t.Errorf("long edit err = %v", err)
	}

	// Spam teaches the blocklists and blocks the commenter; trash.
	if n, err := a.MarkSpam(ctx, []string{b1.String()}, admin.SpamTeach{}, actor.String()); err != nil || n != 1 {
		t.Fatalf("MarkSpam = %d, %v", n, err)
	}
	spamRow, err := a.Comment(ctx, b1.String())
	must(err)
	if spamRow.Status != "spam" || spamRow.Trust != "blocked" {
		t.Errorf("spammed = %+v", spamRow)
	}
	hashes, err := svc.Blocks(ctx, comment.BlockBodyHash)
	must(err)
	if len(hashes) == 0 || hashes[0].Source != "spam" {
		t.Errorf("body_hash blocks = %+v", hashes)
	}
	if n, err := a.Trash(ctx, []string{b2.String()}, actor.String()); err != nil || n != 1 {
		t.Errorf("Trash = %d, %v", n, err)
	}

	// Dead: the queue and counts show it, Restore (Approve) brings it back.
	if _, err := env.db.Pool().Exec(ctx, `UPDATE hatamoto_comments SET status = 'dead', dead_by = 'example' WHERE id = $1`, c2); err != nil {
		t.Fatal(err)
	}
	if cnt, err := a.Counts(ctx); err != nil || cnt.Dead != 1 {
		t.Errorf("Counts.Dead = %d, %v, want 1", cnt.Dead, err)
	}
	if pg, err := a.Queue(ctx, admin.CommentFilter{Status: "dead", PerPage: 10}); err != nil || len(pg.Rows) != 1 || pg.Rows[0].DeadBy != "example" {
		t.Errorf("dead Queue = %+v, %v", pg, err)
	}
	if n, err := a.Approve(ctx, []string{c2.String()}, actor.String()); err != nil || n != 1 {
		t.Errorf("Restore of a dead comment = %d, %v", n, err)
	}
	if row, err := a.Comment(ctx, c2.String()); err != nil || row.Status != "approved" {
		t.Errorf("restored = %+v, %v, want approved", row, err)
	}

	// Entity and type modes.
	if err := a.SetEntityMode(ctx, ent.ID.String(), "closed"); err != nil {
		t.Fatal(err)
	}
	ec, err := a.EntityComments(ctx, ent.ID.String())
	must(err)
	if ec.Override != "closed" || ec.Resolved != "closed" || ec.Total != 5 || ec.Pending != 1 {
		t.Errorf("EntityComments = %+v", ec)
	}
	must(a.SetEntityMode(ctx, ent.ID.String(), ""))
	if len(modes.entities) != 2 || modes.entities[0] != ent.ID || modes.entities[1] != ent.ID || modes.types != 0 {
		t.Errorf("entity mode changes = %v, type changes %d", modes.entities, modes.types)
	}
	if ec, err = a.EntityComments(ctx, ent.ID.String()); err != nil || ec.Override != "" {
		t.Errorf("EntityComments after clear = %+v, %v", ec, err)
	}
	must(a.SetSchemaMode(ctx, sch.ID.String(), "closed"))
	if m, err := a.SchemaMode(ctx, sch.ID.String()); err != nil || m != "closed" {
		t.Errorf("SchemaMode = %q, %v", m, err)
	}
	if kept, err := entities.GetSchema(ctx, sch.ID); err != nil || !strings.Contains(string(kept.Settings), "url_pattern") {
		t.Errorf("schema settings lost url_pattern: %s, %v", kept.Settings, err)
	}
	must(a.SetSchemaMode(ctx, sch.ID.String(), ""))
	if modes.types != 2 || len(modes.entities) != 2 {
		t.Errorf("type mode changes = %d, entity changes %v, want 2 type changes", modes.types, modes.entities)
	}
	if m, err := a.SchemaMode(ctx, sch.ID.String()); err != nil || m != "" {
		t.Errorf("SchemaMode after clear = %q, %v", m, err)
	}
	if err := a.SetSchemaMode(ctx, builtinID.String(), "closed"); !errors.Is(err, admin.ErrCommentsUnsupported) {
		t.Errorf("built-in SetSchemaMode err = %v", err)
	}
	// Per-type community policy keeps the mode and unknown keys.
	must(a.SetSchemaMode(ctx, sch.ID.String(), "closed"))
	tpol := admin.CommentPolicy{Enabled: true, MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}
	must(a.SetSchemaComments(ctx, sch.ID.String(), "oldest", tpol))
	cfgs, err := a.SchemaCommentConfigs(ctx)
	must(err)
	var got *admin.SchemaCommentConfig
	for i := range cfgs {
		if cfgs[i].ID == sch.ID.String() {
			got = &cfgs[i]
		}
	}
	if got == nil || got.Mode != "closed" || got.Order != "oldest" || got.Policy != tpol {
		t.Errorf("SchemaCommentConfigs = %+v", got)
	}
	if kept, err := entities.GetSchema(ctx, sch.ID); err != nil || !strings.Contains(string(kept.Settings), "url_pattern") {
		t.Errorf("schema settings lost url_pattern: %s, %v", kept.Settings, err)
	}
	badPol := tpol
	badPol.MaxDepth = 33
	var pfe *admin.CommentFieldError
	if err := a.SetSchemaComments(ctx, sch.ID.String(), "", badPol); !errors.As(err, &pfe) || pfe.Field != "max_depth" {
		t.Errorf("max_depth 33 err = %v", err)
	}
	if err := a.SetSchemaComments(ctx, builtinID.String(), "", tpol); !errors.Is(err, admin.ErrCommentsUnsupported) {
		t.Errorf("built-in SetSchemaComments err = %v", err)
	}
	if err := a.SetSchemaComments(ctx, uuid.NewString(), "", tpol); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("unknown SetSchemaComments err = %v", err)
	}
	// A schema whose settings are not an object neither hides the other types nor turns a save into a 500.
	var oddID uuid.UUID
	sel(&oddID, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, settings) VALUES ('Cmm Odd', 'cmm-odd', '{"type":"object"}', '[1]') RETURNING id`)
	if cfgs, err := a.SchemaCommentConfigs(ctx); err != nil || len(cfgs) < 2 {
		t.Errorf("SchemaCommentConfigs with a bad schema = %d configs, %v", len(cfgs), err)
	}
	if err := a.SetSchemaComments(ctx, oddID.String(), "", tpol); !errors.As(err, &pfe) || pfe.Field != "order" {
		t.Errorf("bad-settings SetSchemaComments err = %v", err)
	}
	must2 := func(_ any, err error) { must(err) }
	must2(env.db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, oddID))
	must(a.SetSchemaComments(ctx, sch.ID.String(), "", admin.CommentPolicy{MaxDepth: 10, EditWindowMinutes: 15}))
	must(a.SetSchemaMode(ctx, sch.ID.String(), ""))

	opts, err := a.EntityOptions(ctx, 10)
	must(err)
	if len(opts) != 1 || opts[0].ID != ent.ID.String() || opts[0].Title != "Cmm Entity" {
		t.Errorf("EntityOptions = %+v", opts)
	}

	// Commenters (G2): three with names, no ghost; search takes % literally.
	cp, err := a.Commenters(ctx, "", 1)
	must(err)
	names := map[string]bool{}
	for _, r := range cp.Rows {
		names[r.Name] = true
	}
	if len(cp.Rows) != 3 || !names["Alice"] || !names["Bob"] || !names["Cara"] || cp.HasMore {
		t.Errorf("Commenters = %+v", cp)
	}
	if cp, err = a.Commenters(ctx, "ali", 1); err != nil || len(cp.Rows) != 1 || cp.Rows[0].Name != "Alice" {
		t.Errorf("Commenters(ali) = %+v, %v", cp, err)
	}
	if cp, err = a.Commenters(ctx, "%", 1); err != nil || len(cp.Rows) != 0 {
		t.Errorf("Commenters(%%) = %+v, %v", cp, err)
	}

	// Blocklists: hash kinds count only, domains diff.
	lists, err := a.Blocklists(ctx)
	must(err)
	for _, l := range lists {
		if l.Kind == "body_hash" && (l.Count == 0 || len(l.Entries) != 0) {
			t.Errorf("body_hash list = %+v", l)
		}
	}
	must(a.SaveBlocklist(ctx, "link_domain", []string{"x.test", "y.test"}, actor.String()))
	must(a.SaveBlocklist(ctx, "link_domain", []string{"y.test", "z.test"}, actor.String()))
	lists, err = a.Blocklists(ctx)
	must(err)
	for _, l := range lists {
		if l.Kind == "link_domain" && (!slices.Equal(l.Entries, []string{"y.test", "z.test"}) || l.Count != 2) {
			t.Errorf("link_domain list = %+v", l)
		}
	}
	if err := a.SaveBlocklist(ctx, "body_hash", []string{"abc"}, actor.String()); !errors.As(err, &fe) {
		t.Errorf("SaveBlocklist(body_hash, entries) err = %v", err)
	}

	// Sign-out: revoke and block kill an existing hm_c cookie.
	idSvc, err := identity.New(identity.Config{
		DB: env.db, Comments: svc, Mailer: nopMailer{}, Secret: secret,
		Site: func(context.Context) (string, string) { return "QA", "http://example.test" },
	})
	must(err)
	cookie := func(id string) string {
		t.Helper()
		cid, _ := uuid.Parse(id)
		var v int
		sel(&v, `SELECT token_version FROM hatamoto_commenters WHERE id = $1`, cid)
		return identity.SignDevice(identity.DeviceKey(secret), identity.DeviceClaims{CommenterID: cid, TokenVersion: v, Expires: time.Now().Add(time.Hour)})
	}
	trust := func(id string) (s string) {
		sel(&s, `SELECT trust_level FROM hatamoto_commenters WHERE id = $1`, uuid.MustParse(id))
		return s
	}
	old := cookie(aliceID)
	if got, ok := idSvc.Device(ctx, old); !ok || got.String() != aliceID {
		t.Fatalf("Device before revoke = %v, %v", got, ok)
	}
	before := trust(aliceID)
	must(a.RevokeDevices(ctx, aliceID))
	if _, ok := idSvc.Device(ctx, old); ok {
		t.Error("old cookie still valid after RevokeDevices")
	}
	if trust(aliceID) != before {
		t.Error("RevokeDevices changed the trust level")
	}
	if err := a.RevokeDevices(ctx, uuid.NewString()); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("RevokeDevices unknown err = %v", err)
	}
	caraID := ""
	if r, err := a.Comment(ctx, c1.String()); err == nil {
		caraID = r.CommenterID
	}
	cc := cookie(caraID)
	if _, ok := idSvc.Device(ctx, cc); !ok {
		t.Fatal("cara cookie invalid before Block")
	}
	must(a.Block(ctx, caraID, actor.String()))
	if _, ok := idSvc.Device(ctx, cc); ok {
		t.Error("cookie still valid after Block")
	}
	if _, err := a.Vouch(ctx, caraID, actor.String()); !errors.Is(err, admin.ErrCommentBlocked) {
		t.Errorf("Vouch blocked err = %v, want ErrCommentBlocked", err)
	}
	if err := a.Block(ctx, uuid.NewString(), actor.String()); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("Block unknown err = %v", err)
	}

	// Privacy: export has the email and no email or body hashes; erase counts.
	data, err := a.ExportCommenter(ctx, aliceID)
	must(err)
	text := string(data)
	if !strings.Contains(text, "a@cmm.test") || !strings.Contains(text, "edited *text*") {
		t.Errorf("export lacks email or comments: %s", text)
	}
	for _, banned := range []string{"email_hash", "body_hash"} {
		if strings.Contains(text, banned) {
			t.Errorf("export contains %q", banned)
		}
	}
	if _, err := a.ExportCommenter(ctx, uuid.NewString()); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("export unknown err = %v", err)
	}
	if del, anon, err := a.EraseCommenter(ctx, caraID); err != nil || del != 2 || anon != 0 {
		t.Errorf("EraseCommenter = %d, %d, %v, want 2, 0", del, anon, err)
	}

	// Delete a parent and its reply in one call: the reply is already gone.
	if n, err := a.Delete(ctx, []string{a2.String(), replyID}, actor.String()); err != nil || n != 1 {
		t.Errorf("Delete = %d, %v, want 1", n, err)
	}
	if _, err := a.Comment(ctx, replyID); !errors.Is(err, admin.ErrCommentNotFound) {
		t.Errorf("reply still there: %v", err)
	}

	// Export carries the stored network columns (hashes, never the raw IP) and the reply opt-in.
	dres, err := commentfmt.Render("export me")
	must(err)
	dc, err := svc.Store(ctx, comment.Draft{
		EntityID: ent.ID, Email: "d@cmm.test", AuthorName: "Dee", BodyRaw: "export me", BodyHTML: dres.HTML,
		BodyHash: dres.BodyHash, SpamVerdict: "allow", Status: comment.StatusDraft, Locale: "und",
		IP: "203.0.113.7", UserAgent: "TestUA/1", NotifyReplies: true,
	})
	must(err)
	var deeID uuid.UUID
	sel(&deeID, `SELECT commenter_id FROM hatamoto_comments WHERE id = $1`, dc.ID)
	deeData, err := a.ExportCommenter(ctx, deeID.String())
	must(err)
	var deeEx struct {
		Comments []struct {
			IPHash        string   `json:"ip_hash"`
			NetHash       string   `json:"net_hash"`
			UserAgent     string   `json:"user_agent"`
			NotifyReplies bool     `json:"notify_replies"`
			SpamReasons   []string `json:"spam_reasons"`
		} `json:"comments"`
	}
	must(jsonx.Unmarshal(deeData, &deeEx))
	wantIP, wantNet := svc.Keys().HashIP("203.0.113.7")
	if len(deeEx.Comments) != 1 {
		t.Fatalf("export has %d comments, want 1", len(deeEx.Comments))
	}
	if x := deeEx.Comments[0]; x.IPHash == "" || x.IPHash != wantIP || x.NetHash == "" || x.NetHash != wantNet || x.UserAgent != "TestUA/1" || !x.NotifyReplies || x.SpamReasons == nil {
		t.Errorf("export comment = %+v, want hashes %q %q", x, wantIP, wantNet)
	}
	if strings.Contains(string(deeData), "203.0.113.7") {
		t.Errorf("export has the raw IP: %s", deeData)
	}

	for f, want := range map[string]bool{"privacy": true, "blocklists": true, "entity-options": true, "schema-mode": true, "schema-mode-builtin": false} {
		if a.Supports(f) != want {
			t.Errorf("Supports(%q) != %v", f, want)
		}
	}
}
