package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/standing"
)

type fakeStandingStore struct {
	active   *standing.Approval
	approved []standing.Approval
}

func (f *fakeStandingStore) Active(context.Context, string, string) (*standing.Approval, error) {
	if f.active == nil {
		return nil, standing.ErrNoApproval
	}
	return f.active, nil
}

func (f *fakeStandingStore) Approve(_ context.Context, plugin, run string, p standing.Policy, fp standing.Fingerprint, by string) (standing.Approval, error) {
	a := standing.Approval{ID: int64(len(f.approved) + 1), Plugin: plugin, Run: run, Policy: p, FP: fp, ApprovedBy: by}
	f.approved = append(f.approved, a)
	f.active = &a
	return a, nil
}

func (f *fakeStandingStore) Waiting(context.Context, string) ([]standing.WaitingRun, error) {
	return nil, nil
}

type fakeStandingEngine struct {
	verdict    changeset.PolicyVerdict
	calls      []string
	policySeen changeset.StandingPolicy
}

func (f *fakeStandingEngine) Classify(context.Context, int64) error {
	f.calls = append(f.calls, "classify")
	return nil
}

func (f *fakeStandingEngine) CheckPolicy(_ context.Context, _ int64, p changeset.StandingPolicy) (changeset.PolicyVerdict, error) {
	f.calls = append(f.calls, "check")
	f.policySeen = p
	return f.verdict, nil
}

func (f *fakeStandingEngine) PublishReviewed(context.Context, int64) (changeset.PublishResult, error) {
	f.calls = append(f.calls, "publish")
	return changeset.PublishResult{Result: changeset.Result{Applied: 2}}, nil
}

func (f *fakeStandingEngine) MarkAutoPublished(_ context.Context, _, approvalID int64) error {
	f.calls = append(f.calls, "mark")
	return nil
}

const standingManifest = `{"name":"pricer","version":"1.0.0","runs":[{"name":"prices","title":"Prices","cron":"daily",
	"standing_approval":{"fields":["price"],"schemas":["product"],"max_per_hour":50}}],"cron":[{"name":"daily","every":"24h"}]}`

type standingRig struct {
	svc      *standingService
	store    *fakeStandingStore
	eng      *fakeStandingEngine
	manifest *json.RawMessage
	version  *string
	schema   *string
	audits   *[]string
}

func newStandingRig() standingRig {
	manifest, version, schema := json.RawMessage(standingManifest), "1.0.0", `{"type":"object"}`
	var audits []string
	r := standingRig{store: &fakeStandingStore{}, eng: &fakeStandingEngine{verdict: changeset.PolicyVerdict{OK: true}},
		manifest: &manifest, version: &version, schema: &schema, audits: &audits}
	r.svc = &standingService{
		store: r.store, eng: r.eng, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		plugin: func(string) (string, json.RawMessage, bool) { return version, manifest, true },
		schemas: func(context.Context, string) (json.RawMessage, bool, error) {
			return json.RawMessage(schema), true, nil
		},
		audit: func(_ context.Context, action, _ string, _ map[string]any) { audits = append(audits, action) },
	}
	return r
}

// A manifest that declares a rule enables nothing: without an approval row,
// created only by Approve, the cron tick starts no run and nothing publishes.
func TestApprovalOnlyFromOwnerUI(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	gate := standingGate{r.svc}
	if _, ok := r.svc.Policy("pricer"); !ok {
		t.Fatal("the manifest rule should be readable")
	}
	if on, err := gate.Active(ctx, "pricer", "prices"); err != nil || on {
		t.Fatalf("Active with only a manifest = %v, %v; want false", on, err)
	}
	if err := gate.AutoPublish(ctx, "pricer", "prices", 7); err != nil || len(r.eng.calls) != 0 || len(r.store.approved) != 0 {
		t.Fatalf("AutoPublish without approval: err=%v calls=%v approved=%d", err, r.eng.calls, len(r.store.approved))
	}
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner"); err != nil {
		t.Fatal(err)
	}
	if on, _ := gate.Active(ctx, "pricer", "prices"); !on || len(r.store.approved) != 1 {
		t.Fatalf("Approve should create the one active approval, got %d", len(r.store.approved))
	}
	got := r.store.approved[0]
	if got.ApprovedBy != "owner" || got.Policy.MaxPerHour != 50 || got.Policy.Fields[0] != "price" || got.FP.SchemaHashes["product"] == "" {
		t.Fatalf("approval must snapshot the manifest rule and fingerprint, got %+v", got)
	}
}

func TestApproveRefusesARunWithoutARule(t *testing.T) {
	r := newStandingRig()
	if err := r.svc.Approve(context.Background(), "pricer", "other", "owner"); !errors.Is(err, ErrNoStandingRule) {
		t.Fatalf("Approve of an undeclared run = %v, want ErrNoStandingRule", err)
	}
	*r.manifest = json.RawMessage(`{"name":"pricer","version":"1.0.0"}`)
	if err := r.svc.Approve(context.Background(), "pricer", "prices", "owner"); !errors.Is(err, ErrNoStandingRule) {
		t.Fatalf("Approve without a rule = %v, want ErrNoStandingRule", err)
	}
	if len(r.store.approved) != 0 {
		t.Fatal("no approval may be created")
	}
}

func TestAutoPublishInsidePolicyMarksAutomatic(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.autoPublish(ctx, "pricer", "prices", 9); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.eng.calls, ","); got != "classify,check,publish,mark" {
		t.Fatalf("call order = %s, want classify,check,publish,mark", got)
	}
	if r.eng.policySeen.Plugin != "pricer" || r.eng.policySeen.MaxPerHour != 50 {
		t.Fatalf("policy passed to the check = %+v", r.eng.policySeen)
	}
	if last := (*r.audits)[len(*r.audits)-1]; last != "changeset.publish_auto" {
		t.Fatalf("last audit = %s", last)
	}
}

func TestAutoPublishWaitsWhenStaleOrOutsidePolicy(t *testing.T) {
	for name, mutate := range map[string]func(r standingRig){
		"version changed": func(r standingRig) { *r.version = "1.1.0" },
		"manifest changed": func(r standingRig) {
			*r.manifest = json.RawMessage(strings.Replace(standingManifest, "Prices", "Rates", 1))
		},
		"schema changed": func(r standingRig) { *r.schema = `{"type":"object","properties":{"x":{}}}` },
		"outside policy": func(r standingRig) { r.eng.verdict = changeset.PolicyVerdict{Reasons: []string{"Change 1: text"}} },
	} {
		t.Run(name, func(t *testing.T) {
			r := newStandingRig()
			ctx := context.Background()
			if err := r.svc.Approve(ctx, "pricer", "prices", "owner"); err != nil {
				t.Fatal(err)
			}
			mutate(r)
			if err := r.svc.autoPublish(ctx, "pricer", "prices", 9); err != nil {
				t.Fatalf("a refusal is not an error: %v", err)
			}
			for _, c := range r.eng.calls {
				if c == "publish" || c == "mark" {
					t.Fatalf("published despite refusal: %v", r.eng.calls)
				}
			}
			if last := (*r.audits)[len(*r.audits)-1]; last != "changeset.publish_auto_refused" {
				t.Fatalf("last audit = %s", last)
			}
		})
	}
}

// The admin adapter reports the rule the manifest declares and the approval
// Approve recorded, and records one only through Approve.
func TestStandingAdminMapsRuleAndApproval(t *testing.T) {
	r := newStandingRig()
	a := standingAdmin{svc: func() *standingService { return r.svc }}
	ctx := context.Background()
	rule, ok := a.Rule("pricer")
	if !ok || rule.Run != "prices" || rule.MaxPerHour != 50 || rule.Fields[0] != "price" || rule.Schemas[0] != "product" {
		t.Fatalf("Rule = %+v, %v", rule, ok)
	}
	if got, err := a.Active(ctx, "pricer", "prices"); err != nil || got != nil {
		t.Fatalf("Active before approving = %+v, %v", got, err)
	}
	if err := a.Approve(ctx, "pricer", "prices", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Active(ctx, "pricer", "prices"); err != nil || got == nil || got.By != "owner@example.com" {
		t.Fatalf("Active after approving = %+v, %v", got, err)
	}
	none := standingAdmin{svc: func() *standingService { return nil }}
	if _, ok := none.Rule("pricer"); ok {
		t.Fatal("no rule without the service")
	}
	if err := none.Approve(ctx, "pricer", "prices", "x"); !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("Approve without the service = %v", err)
	}
}
