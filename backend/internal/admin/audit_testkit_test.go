package admin

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// auditCapture records every audit event the admin writes and returns a reader
// of a snapshot. It replaces the wired sink.
func auditCapture(a *Admin) func() []AuditEvent {
	var (
		mu  sync.Mutex
		evs []AuditEvent
	)
	a.deps.Audit = func(_ context.Context, e AuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		evs = append(evs, e)
	}
	return func() []AuditEvent {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(evs)
	}
}

// auditDetailKeys is the allow-list of detail keys per action. An action that
// is not here fails assertAuditSafe.
var auditDetailKeys = map[string][]string{
	"session.sign_in":          {"method", "via", "provider", "step"},
	"session.sign_in_failed":   {"reason", "method", "via", "provider"},
	"session.sign_out":         {"all", "via"},
	"entity.create":            {"name", "slug", "status", "schema", "via", "builder"},
	"entity.publish_scheduled": {"name", "slug", "schema"},
	"entity.links_inserted":    {"count"},
	"entity.terms_replaced":    {"count", "via"},
	"user.created":             {"role", "via", "invite"},
	"user.role_changed":        {"from", "to", "via"},
	"user.updated":             {"changed", "via"},
	"user.deleted":             {"role", "via"},
	"user.invited":             {"role", "via"},
	"user.invite_resent":       {"role"},
	"user.invite_revoked":      {},
	"user.invite_accepted":     {"role"},
	"user.reset_link_sent":     {"via"},
	"user.password_reset":      {"via"},
	"user.password_changed":    {"via"},
	"user.identity_linked":     {"issuer"},
	"user.identity_unlinked":   {"issuer"},
	"user.sessions_revoked":    {"keys", "via"},
	"api_key.created":          {"name", "scopes", "expires", "prefix", "via"},
	"apikey.create":            {"name", "scopes", "prefix"},
	"media.rebuild.start":      {"run_id", "images"},
	"media.rebuild.finish":     {"run_id", "images", "skipped"},
	"package.grants":           {"from", "to", "kept"},
	"api_key.updated":          {"name", "changed", "via"},
	"api_key.revoked":          {"name", "via"},
	"api_key.regenerated":      {"name", "prefix", "via"},
	"api_key.deleted":          {"name", "via"},
	"settings.updated":         {"changed", "op", "via"},
	"template.saved":           {"name"},
	"template.deleted":         {"name"},
	"webhook.created":          {"name", "events", "changed"},
	"webhook.updated":          {"name", "events", "changed"},
	"webhook.paused":           {"name", "events", "changed"},
	"webhook.resumed":          {"name", "events", "changed"},
	"webhook.secret_rotated":   {"name", "events", "changed"},
	"webhook.deleted":          {"name", "events", "changed"},
	"content_type.created":     {"name", "changed"},
	"content_type.updated":     {"name", "changed"},
	"content_type.deleted":     {"name", "changed"},
	"linking.rule_saved":       {"enabled", "op", "count"},
	"linking.rule_toggled":     {"enabled", "op", "count"},
	"linking.rule_deleted":     {"enabled", "op", "count"},
	"linking.rules_changed":    {"enabled", "op", "count"},
	"term.created":             {"name", "slug", "kind", "locale", "into", "via"},
	"term.updated":             {"name", "slug", "kind", "locale", "into", "via"},
	"term.deleted":             {"name", "slug", "kind", "locale", "into", "via"},
	"term.merged":              {"name", "slug", "kind", "locale", "into", "via"},
	"term.copied":              {"name", "slug", "kind", "locale", "into", "via"},
	"term.linked":              {"name", "slug", "kind", "locale", "into", "via"},
	"term.unlinked":            {"name", "slug", "kind", "locale", "into", "via"},
	"menu.created":             {"name", "location", "items", "via"},
	"menu.updated":             {"name", "location", "items", "via"},
	"menu.deleted":             {"name", "location", "items", "via"},
	"menu.item_created":        {"name", "location", "items", "via"},
	"menu.item_updated":        {"name", "location", "items", "via"},
	"menu.item_deleted":        {"name", "location", "items", "via"},
	"menu.items_reordered":     {"name", "location", "items", "via"},
	"redirect.created":         {"source", "target", "status_code", "enabled", "via"},
	"redirect.updated":         {"source", "target", "status_code", "enabled", "via"},
	"redirect.deleted":         {"source", "target", "status_code", "enabled", "via"},
	"not_found.ignored":        {"path", "via"},
	"not_found.deleted":        {"path", "via"},
	"media.uploaded":           {"name", "mime", "size", "folder", "count", "bytes", "via"},
	"media.updated":            {"name", "mime", "size", "folder", "count", "bytes", "via"},
	"media.deleted":            {"name", "mime", "size", "folder", "count", "bytes", "via"},
	"media.swept":              {"name", "mime", "size", "folder", "count", "bytes", "via"},
	"media.adopted":            {"name", "mime", "size", "folder", "count", "bytes", "via"},
	"entity.publish":           {"name", "slug", "status", "to", "from", "publish_at", "took_ownership", "via", "schema", "bulk", "locale", "builder", "count"},
	"entity.unpublish":         {"name", "slug", "status", "to", "from", "publish_at", "took_ownership", "via", "schema", "bulk", "locale", "builder", "count"},
	"entity.archive":           {"name", "slug", "status", "to", "from", "publish_at", "took_ownership", "via", "schema", "bulk", "locale", "builder", "count"},
	"entity.update":            {"name", "slug", "status", "to", "from", "publish_at", "took_ownership", "via", "schema", "bulk", "locale", "builder", "count"},
	"entity.schedule":          {"name", "slug", "status", "to", "from", "publish_at", "took_ownership", "via", "schema", "bulk", "locale", "builder", "count"},
	"entity.delete":            {"name", "slug", "status", "to", "from", "publish_at", "took_ownership", "via", "schema", "bulk", "locale", "builder", "count"},
}

// auditDenyKeys never appear in a detail map, whatever the action.
var auditDenyKeys = []string{"password", "secret", "token", "code", "key", "plain_key", "url", "subject"}

// assertAuditSafe fails the test when an event carries a detail key outside
// its action's allow-list, a denied key, a nested map, or any of secrets.
func assertAuditSafe(t testing.TB, evs []AuditEvent, secrets ...string) {
	t.Helper()
	for _, ev := range evs {
		allowed, known := auditDetailKeys[ev.Action]
		if !known {
			t.Errorf("audit action %q has no entry in auditDetailKeys", ev.Action)
		}
		for k, v := range ev.Detail {
			if known && !slices.Contains(allowed, k) {
				t.Errorf("%s: detail key %q is not in the allow-list %v", ev.Action, k, allowed)
			}
			if slices.Contains(auditDenyKeys, k) {
				t.Errorf("%s: detail key %q is denied", ev.Action, k)
			}
			if k == "email" && ev.Action == AuditSignInFailed {
				t.Errorf("%s: detail carries the typed email", ev.Action)
			}
			if auditNested(v) {
				t.Errorf("%s: detail key %q holds a nested map", ev.Action, k)
			}
		}
		dump := fmt.Sprintf("%#v", ev)
		for _, s := range secrets {
			if s != "" && strings.Contains(dump, s) {
				t.Errorf("%s: event contains a secret", ev.Action)
			}
		}
	}
}

// auditNested reports whether v is a map, or a slice holding one.
func auditNested(v any) bool {
	switch x := v.(type) {
	case map[string]any, map[string]string:
		return true
	case []map[string]any:
		return true
	case []any:
		return slices.ContainsFunc(x, auditNested)
	}
	return false
}

// auditFailRecorder is a testing.TB that records failures instead of failing.
type auditFailRecorder struct {
	testing.TB
	failed bool
}

func (f *auditFailRecorder) Helper()               {}
func (f *auditFailRecorder) Errorf(string, ...any) { f.failed = true }

func TestAuditKitFlagsUnsafeEvents(t *testing.T) {
	t.Parallel()

	ok := AuditEvent{Action: AuditSignInFailed, Detail: map[string]any{"reason": "bad_password", "method": "password"}}
	cases := []struct {
		name    string
		ev      AuditEvent
		secrets []string
		fails   bool
	}{
		{"safe", ok, []string{"hunter2"}, false},
		{"denied key", AuditEvent{Action: AuditSignIn, Detail: map[string]any{"password": "x"}}, nil, true},
		{"key outside the allow-list", AuditEvent{Action: AuditSignOut, Detail: map[string]any{"name": "x"}}, nil, true},
		{"unknown action", AuditEvent{Action: "nope.nothing"}, nil, true},
		{"typed email", AuditEvent{Action: AuditSignInFailed, Detail: map[string]any{"email": "a@b.c"}}, nil, true},
		{"nested map", AuditEvent{Action: AuditSettingsUpdated, Detail: map[string]any{"changed": map[string]any{"a": 1}}}, nil, true},
		{"secret in actor", AuditEvent{Action: AuditSignOut, Actor: "hunter2"}, []string{"hunter2"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &auditFailRecorder{TB: t}
			assertAuditSafe(f, []AuditEvent{tc.ev}, tc.secrets...)
			if f.failed != tc.fails {
				t.Fatalf("failed = %v, want %v", f.failed, tc.fails)
			}
		})
	}
}

func TestAuditKitCapturesEvents(t *testing.T) {
	t.Parallel()

	a := &Admin{}
	read := auditCapture(a)
	a.deps.Audit(context.Background(), AuditEvent{Action: AuditSignIn})
	if got := read(); len(got) != 1 || got[0].Action != AuditSignIn {
		t.Fatalf("captured %+v", got)
	}
}

// newAuditActions are the actions this change adds; each needs a label.
var newAuditActions = []string{
	"session.sign_in",
	"session.sign_in_failed",
	"session.sign_out",
	"entity.create",
	"entity.publish_scheduled",
	"entity.links_inserted",
	"entity.terms_replaced",
	"user.created",
	"user.role_changed",
	"user.updated",
	"user.deleted",
	"user.invited",
	"user.invite_resent",
	"user.invite_revoked",
	"user.invite_accepted",
	"user.reset_link_sent",
	"user.password_reset",
	"user.password_changed",
	"user.identity_linked",
	"user.identity_unlinked",
	"user.sessions_revoked",
	"api_key.created",
	"apikey.create", // written by the CLI before api_key.created existed
	"api_key.updated",
	"api_key.revoked",
	"api_key.regenerated",
	"api_key.deleted",
	"settings.updated",
	"package.grants",
	"webhook.created",
	"webhook.updated",
	"webhook.paused",
	"webhook.resumed",
	"webhook.secret_rotated",
	"webhook.deleted",
	"content_type.created",
	"content_type.updated",
	"content_type.deleted",
	"template.saved",
	"template.deleted",
	"linking.rule_saved",
	"linking.rule_toggled",
	"linking.rule_deleted",
	"linking.rules_changed",
	"term.created",
	"term.updated",
	"term.deleted",
	"term.merged",
	"term.copied",
	"term.linked",
	"term.unlinked",
	"menu.created",
	"menu.updated",
	"menu.deleted",
	"menu.item_created",
	"menu.item_updated",
	"menu.item_deleted",
	"menu.items_reordered",
	"redirect.created",
	"redirect.updated",
	"redirect.deleted",
	"not_found.ignored",
	"not_found.deleted",
	"media.uploaded",
	"media.updated",
	"media.deleted",
	"media.rebuild.start",
	"media.rebuild.finish",
	"media.swept",
	"media.adopted",
}
