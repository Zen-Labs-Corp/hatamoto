package pluginhost

import (
	"context"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

func mailQuotaAPI(t *testing.T, svc Services, granted ...string) *API {
	t.Helper()
	g := NewGrantStore(nil)
	for _, p := range granted {
		if err := g.Set(context.Background(), p, []sdk.Permission{sdk.PermMailSend}); err != nil {
			t.Fatal(err)
		}
	}
	return NewAPI(svc, g, func(p string) (sdk.Manifest, bool) {
		return sdk.Manifest{Name: p, Permissions: []sdk.Permission{sdk.PermMailSend}}, true
	}, nil)
}

func callPermissions(t *testing.T, a *API, plugin string) map[string]any {
	t.Helper()
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: "permissions"})
	var out struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if err := jsonx.UnmarshalPinned(a.Call(context.Background(), plugin, in), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("permissions failed: %+v", out)
	}
	return out.Result
}

func TestMailQuotaDefaultKeepsOldLimiter(t *testing.T) {
	t.Parallel()
	a := mailQuotaAPI(t, Services{Mail: &countingMailer{}}, "p")
	if q, sent := a.MailQuotaState("p"); q != 360 || sent != 0 {
		t.Fatalf("state = %d, %d", q, sent)
	}
	l := a.limits("p").mail
	if l.Limit() != 0.1 || l.Burst() != 10 {
		t.Fatalf("limiter = %v / %d, want 0.1 / 10", l.Limit(), l.Burst())
	}
}

func TestMailQuotaLiveUpdate(t *testing.T) {
	t.Parallel()
	mailer := &countingMailer{}
	a := mailQuotaAPI(t, Services{Mail: mailer}, "p", "other")
	if out := callMail(t, a, "p", 10); !out.OK { // creates the limiter, burst 10 drained
		t.Fatalf("%+v", out.Error)
	}
	a.SetMailQuota("p", 3600)
	l := a.limits("p").mail
	if l.Limit() != 1 || l.Burst() != 100 {
		t.Fatalf("limiter = %v / %d, want 1 / 100", l.Limit(), l.Burst())
	}
	if q, sent := a.MailQuotaState("p"); q != 3600 || sent != 10 {
		t.Fatalf("state = %d, %d", q, sent)
	}
	if other := a.limits("other").mail; other.Burst() != 10 {
		t.Fatalf("other plugin burst = %d", other.Burst())
	}
	// Raising the burst does not add tokens: the drained bucket stays empty.
	if out := callMail(t, a, "p", 1); out.OK {
		t.Fatal("raising the quota refilled the bucket")
	}
	// A plugin whose stored quota is 3600 when its limiter is built gets the full burst of 100.
	store := NewMailQuotaStore(nil)
	if err := store.SetMailPerHour(context.Background(), "fresh", 3600); err != nil {
		t.Fatal(err)
	}
	a = mailQuotaAPI(t, Services{Mail: mailer, MailQuotas: store}, "fresh")
	for i := 0; i < 10; i++ {
		if out := callMail(t, a, "fresh", 10); !out.OK {
			t.Fatalf("call %d: %+v", i, out.Error)
		}
	}
	out := callMail(t, a, "fresh", 1)
	if out.OK || out.Error.Code != sdk.ErrRateLimited || !strings.Contains(out.Error.Message, "mail quota (3600 recipients per hour) reached") {
		t.Fatalf("101st recipient: %+v", out)
	}
}

func TestMailQuotaMessageUsesLiveNumber(t *testing.T) {
	t.Parallel()
	a := mailQuotaAPI(t, Services{Mail: &countingMailer{}}, "p")
	if out := callMail(t, a, "p", 10); !out.OK {
		t.Fatal(out.Error)
	}
	out := callMail(t, a, "p", 1)
	if out.OK || !strings.Contains(out.Error.Message, "mail quota (360 recipients per hour) reached") {
		t.Fatalf("%+v", out)
	}
	a.SetMailQuota("p", 36)
	out = callMail(t, a, "p", 1)
	if out.OK || !strings.Contains(out.Error.Message, "(36 recipients per hour)") {
		t.Fatalf("%+v", out)
	}
}

func TestMailQuotaInPermissions(t *testing.T) {
	t.Parallel()
	store := NewMailQuotaStore(nil)
	if err := store.SetMailPerHour(context.Background(), "p", 720); err != nil {
		t.Fatal(err)
	}
	a := mailQuotaAPI(t, Services{MailQuotas: store}, "p")
	res := callPermissions(t, a, "p")
	mail, _ := res["mail"].(map[string]any)
	if mail["recipients_per_hour"] != float64(720) {
		t.Fatalf("mail = %v", res["mail"])
	}
	if res["granted"] == nil || res["requested"] == nil {
		t.Fatalf("granted/requested changed: %v", res)
	}
	a.SetMailQuota("p", 3600)
	if mail, _ := callPermissions(t, a, "p")["mail"].(map[string]any); mail["recipients_per_hour"] != float64(3600) {
		t.Fatalf("not live: %v", mail)
	}
	// Requested but not granted: no mail key.
	if _, ok := callPermissions(t, a, "nogrant")["mail"]; ok {
		t.Fatal("mail present without the grant")
	}
}

func TestMailQuotaRebuiltAPIPullsFromStore(t *testing.T) {
	t.Parallel()
	store := NewMailQuotaStore(nil)
	if err := store.SetMailPerHour(context.Background(), "p", 3600); err != nil {
		t.Fatal(err)
	}
	a := mailQuotaAPI(t, Services{MailQuotas: store}, "p") // what Configure builds
	if q, _ := a.MailQuotaState("p"); q != 3600 {
		t.Fatalf("quota = %d", q)
	}
	if b := a.limits("p").mail.Burst(); b != 100 {
		t.Fatalf("burst = %d", b)
	}
}

func TestBurstFor(t *testing.T) {
	for n, want := range map[int]int{1: 10, 360: 10, 720: 20, 3600: 100, 3601: 100} {
		if got := burstFor(n); got != want {
			t.Errorf("burstFor(%d) = %d, want %d", n, got, want)
		}
	}
}

// granted is a subset of requested, also for the camera grant: requested lists
// everything the manifest asks for (Asked), not only its permissions.
func TestPermissionsRequestedCoversCameraGrant(t *testing.T) {
	t.Parallel()
	g := NewGrantStore(nil)
	if err := g.Set(context.Background(), "p", []sdk.Permission{sdk.PermKV, sdk.PermUICamera}); err != nil {
		t.Fatal(err)
	}
	a := NewAPI(Services{}, g, func(p string) (sdk.Manifest, bool) {
		return sdk.Manifest{Name: p, Permissions: []sdk.Permission{sdk.PermKV}, UI: &sdk.AdminUI{Allow: []string{sdk.UIAllowCamera}}}, true
	}, nil)
	res := callPermissions(t, a, "p")
	requested, _ := res["requested"].([]any)
	for _, want := range []any{"kv", "ui.allow:camera"} {
		if !slices.Contains(requested, want) {
			t.Errorf("requested %v lacks %v", requested, want)
		}
	}
	if len(requested) != 2 {
		t.Errorf("requested = %v", requested)
	}
}
