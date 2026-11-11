package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
)

// The mail quota is set by the owner through the admin only: a plugin settings
// write with a key of the same name leaves it alone, and saving it updates the
// running plugin without a reload.
func TestPluginMailQuotaLiveAndSeparateFromSettings(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), hooks: hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	rt, err := pluginhost.NewRuntime(s.logger, s.hooks, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	quotas := pluginhost.NewMailQuotaStore(nil)
	rt.Configure(pluginhost.Config{MailQuotas: quotas})
	m := capaManifest()
	m.SettingsSchema = jsontext.Value(`{"type":"object","properties":{"mail_per_hour":{"type":"integer"}}}`)
	if st := rt.Load(context.Background(), m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatal(st.Error)
	}
	settings := &memSettings{data: map[string]jsontext.Value{}}
	s.plugins, s.mailQuotas, s.pluginSettingsStore = rt, quotas, settings
	pa := pluginAdmin{s: s}
	ctx := context.Background()

	if q, sent := pa.MailQuota("capa"); q != 360 || sent != 0 {
		t.Fatalf("default state = %d, %d", q, sent)
	}

	// A settings write cannot reach the quota.
	if errs, err := pa.SaveSettings(ctx, "capa", map[string]any{"mail_per_hour": 3600}); err != nil || len(errs) > 0 {
		t.Fatalf("save settings: %v %v", errs, err)
	}
	if q, _ := pa.MailQuota("capa"); q != 360 {
		t.Fatalf("a settings write changed the live quota to %d", q)
	}
	if got := quotas.MailPerHour("capa"); got != 360 {
		t.Fatalf("a settings write changed the stored quota to %d", got)
	}

	// The owner's save is stored and live at once.
	if err := pa.SetMailQuota(ctx, "capa", 3600); err != nil {
		t.Fatal(err)
	}
	if q, _ := pa.MailQuota("capa"); q != 3600 {
		t.Fatalf("live quota = %d, want 3600", q)
	}
	if got := quotas.MailPerHour("capa"); got != 3600 {
		t.Fatalf("stored quota = %d, want 3600", got)
	}
	if err := pa.SetMailQuota(ctx, "capa", 3601); err == nil {
		t.Fatal("3601 was accepted")
	}
	if q, _ := pa.MailQuota("capa"); q != 3600 {
		t.Fatalf("a refused value changed the quota to %d", q)
	}

	// Uninstall returns the plugin to the default.
	s.resetMailQuota("capa")
	if q, _ := pa.MailQuota("capa"); q != 360 || quotas.MailPerHour("capa") != 360 {
		t.Fatalf("after uninstall: live %d, stored %d", q, quotas.MailPerHour("capa"))
	}
}
