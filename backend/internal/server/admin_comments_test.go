package server

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
)

func TestCommentsAdminSettingsMapping(t *testing.T) {
	in := admin.CommentSettings{
		Enabled: true, KnownMinApproved: 2, KnownMinAgeDays: 3, TrustedMinApproved: 4, TrustedMinAgeDays: 5,
		AutoCloseDays: 6, MaxDepth: 7, PageSize: 8, Sort: "oldest", SlowModeSeconds: 9, Captcha: true,
		CookieDays: 10, PurgeDays: 11,
	}
	set := toSettings(in)
	if set.DeviceCookieDays != 10 || set.Thresholds.KnownDays != 3 || set.Thresholds.TrustedApproved != 4 || set.Sort != comment.SortOldest {
		t.Fatalf("toSettings = %+v", set)
	}
	if got := fromSettings(set); got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
}

func TestCommentsAdminFieldErrorMapping(t *testing.T) {
	for in, want := range map[string]string{
		"thresholds.known_approved":   "known_min_approved",
		"thresholds.known_days":       "known_min_age_days",
		"thresholds.trusted_approved": "trusted_min_approved",
		"thresholds.trusted_days":     "trusted_min_age_days",
		"device_cookie_days":          "cookie_days",
		"page_size":                   "page_size",
	} {
		err := mapCommentErr(&comment.FieldError{Field: in, Message: "bad"})
		fe, ok := err.(*admin.CommentFieldError)
		if !ok || fe.Field != want || fe.Message != "bad" {
			t.Errorf("%s -> %#v, want field %s", in, err, want)
		}
	}
	if mapCommentErr(comment.ErrNotFound) != admin.ErrCommentNotFound {
		t.Error("ErrNotFound not mapped")
	}
}

func TestCommentsAdminToRowDropsHashes(t *testing.T) {
	cid := uuid.New()
	row := toRow(comment.QueueItem{
		Comment: comment.Comment{
			ID: uuid.New(), EntityID: uuid.New(), CommenterID: &cid, Status: comment.StatusPending,
			IPHash: "iphash-secret", NetHash: "nethash-secret", UserAgent: "agent-secret",
			BodyHash: "bodyhash-secret", LinkDomains: []string{"linkdomain-secret.test"},
			SpamScore: 3, SpamReasons: []string{"contains a link"}, CreatedAt: time.Now(),
		},
		Trust: comment.TrustKnown, ApprovedCount: 2,
	})
	v := reflect.ValueOf(row)
	for i := range v.NumField() {
		if s := reflect.ValueOf(v.Field(i).Interface()); s.Kind() == reflect.String && strings.Contains(s.String(), "secret") {
			t.Errorf("field %s leaks %q", v.Type().Field(i).Name, s.String())
		}
	}
	if row.CommenterID != cid.String() || row.Score != 3 || row.Trust != "known" || row.ApprovedCount != 2 || row.Reasons[0] != "contains a link" {
		t.Errorf("row = %+v", row)
	}
}

func TestCommentsAdminNextTrust(t *testing.T) {
	for in, want := range map[comment.TrustLevel]comment.TrustLevel{
		comment.TrustNew:     comment.TrustKnown,
		comment.TrustKnown:   comment.TrustTrusted,
		comment.TrustTrusted: comment.TrustTrusted,
	} {
		if got := nextTrust(in); got != want {
			t.Errorf("nextTrust(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestCommentsAdminSupports(t *testing.T) {
	a := commentsAdmin{}
	for f, want := range map[string]bool{
		"privacy": true, "blocklists": true, "entity-options": true, "schema-mode": true,
		"schema-mode-builtin": false, "other": false,
	} {
		if got := a.Supports(f); got != want {
			t.Errorf("Supports(%q) = %v, want %v", f, got, want)
		}
	}
}

func TestCommentsAdminReplyDraft(t *testing.T) {
	raw := "Thanks <b>all</b>\nsee https://example.com/x"
	res, err := renderBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := commentfmt.Render(raw); res.HTML != want.HTML {
		t.Fatal("renderBody differs from commentfmt.Render")
	}
	ent, parent, author := uuid.New(), uuid.New(), uuid.New()
	d := replyDraft(ent, parent, author, "Site", "und", res, raw)
	if d.Status != comment.StatusApproved || d.AuthorUserID == nil || *d.AuthorUserID != author ||
		d.ParentID == nil || *d.ParentID != parent || d.EntityID != ent {
		t.Fatalf("draft = %+v", d)
	}
	if d.BodyHTML != res.HTML || d.BodyRaw != raw || !d.HasLinks || d.Locale != "und" || d.Email != "" || d.IP != "" {
		t.Fatalf("draft body = %+v", d)
	}
	if _, err := renderBody("   "); err != admin.ErrCommentEmpty {
		t.Errorf("empty body err = %v", err)
	}
}
