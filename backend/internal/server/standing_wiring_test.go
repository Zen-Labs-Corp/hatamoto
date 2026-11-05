package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/standing"
)

type fakeStandingStore struct {
	active   *standing.Approval
	voided   *standing.Voided
	approved []standing.Approval
	waiting  []standing.WaitingRun
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
	return f.waiting, nil
}

func (f *fakeStandingStore) Void(_ context.Context, _, reason string) (int64, error) {
	var n int64
	if f.active != nil {
		n = 1
	}
	f.active, f.voided = nil, &standing.Voided{Reason: reason}
	return n, nil
}

func (f *fakeStandingStore) LastVoided(context.Context, string, string) (*standing.Voided, error) {
	return f.voided, nil
}

type fakeStandingEngine struct {
	verdict    changeset.PolicyVerdict
	calls      []string
	policySeen changeset.StandingPolicy
	approvalID int64
	published  bool // the engine published (the verdict was OK)
}

func (f *fakeStandingEngine) Classify(context.Context, int64) error {
	f.calls = append(f.calls, "classify")
	return nil
}

func (f *fakeStandingEngine) PublishUnderPolicy(_ context.Context, _ int64, p changeset.StandingPolicy, approvalID int64) (changeset.PolicyVerdict, changeset.PublishResult, error) {
	f.calls = append(f.calls, "publish")
	f.policySeen, f.approvalID = p, approvalID
	if !f.verdict.OK {
		return f.verdict, changeset.PublishResult{}, nil
	}
	f.published = true
	return f.verdict, changeset.PublishResult{Result: changeset.Result{Applied: 2}}, nil
}

// standingHash is what the tab posts back: the hash of the manifest it showed.
var standingHash, _ = standing.ManifestHash(jsontext.Value(standingManifest))

const standingManifest = `{"name":"pricer","version":"1.0.0","runs":[{"name":"prices","title":"Prices","cron":"daily",
	"standing_approval":{"fields":["price"],"schemas":["product"],"max_per_hour":50}}],"cron":[{"name":"daily","every":"24h"}]}`

type standingRig struct {
	svc      *standingService
	store    *fakeStandingStore
	eng      *fakeStandingEngine
	manifest *jsontext.Value
	version  *string
	schema   *string
	audits   *[]string
}

func newStandingRig() standingRig {
	manifest, version, schema := jsontext.Value(standingManifest), "1.0.0", `{"type":"object"}`
	var audits []string
	r := standingRig{store: &fakeStandingStore{}, eng: &fakeStandingEngine{verdict: changeset.PolicyVerdict{OK: true}},
		manifest: &manifest, version: &version, schema: &schema, audits: &audits}
	r.svc = &standingService{
		store: r.store, eng: r.eng, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		plugin: func(string) (string, jsontext.Value, bool) { return version, manifest, true },
		schemas: func(context.Context, string) (jsontext.Value, bool, error) {
			return jsontext.Value(schema), true, nil
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
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
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
	if err := r.svc.Approve(context.Background(), "pricer", "other", "owner", "1.0.0", standingHash); !errors.Is(err, ErrNoStandingRule) {
		t.Fatalf("Approve of an undeclared run = %v, want ErrNoStandingRule", err)
	}
	*r.manifest = jsontext.Value(`{"name":"pricer","version":"1.0.0"}`)
	if err := r.svc.Approve(context.Background(), "pricer", "prices", "owner", "1.0.0", standingHash); !errors.Is(err, ErrNoStandingRule) {
		t.Fatalf("Approve without a rule = %v, want ErrNoStandingRule", err)
	}
	if len(r.store.approved) != 0 {
		t.Fatal("no approval may be created")
	}
}

func TestAutoPublishInsidePolicyMarksAutomatic(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.autoPublish(ctx, "pricer", "prices", 9); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.eng.calls, ","); got != "classify,publish" {
		t.Fatalf("call order = %s, want classify,publish", got)
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
			*r.manifest = jsontext.Value(strings.Replace(standingManifest, "Prices", "Rates", 1))
		},
		"schema changed": func(r standingRig) { *r.schema = `{"type":"object","properties":{"x":{}}}` },
		"outside policy": func(r standingRig) { r.eng.verdict = changeset.PolicyVerdict{Reasons: []string{"Change 1: text"}} },
	} {
		t.Run(name, func(t *testing.T) {
			r := newStandingRig()
			ctx := context.Background()
			if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
				t.Fatal(err)
			}
			mutate(r)
			if err := r.svc.autoPublish(ctx, "pricer", "prices", 9); err != nil {
				t.Fatalf("a refusal is not an error: %v", err)
			}
			if r.eng.published {
				t.Fatalf("published despite refusal: %v", r.eng.calls)
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
	if err := a.Approve(ctx, "pricer", "prices", "owner@example.com", "1.0.0", standingHash); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Active(ctx, "pricer", "prices"); err != nil || got == nil || got.By != "owner@example.com" {
		t.Fatalf("Active after approving = %+v, %v", got, err)
	}
	none := standingAdmin{svc: func() *standingService { return nil }}
	if _, ok := none.Rule("pricer"); ok {
		t.Fatal("no rule without the service")
	}
	if err := none.Approve(ctx, "pricer", "prices", "x", "1.0.0", standingHash); !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("Approve without the service = %v", err)
	}
}

func TestStandingAdminPausedAfterVoid(t *testing.T) {
	r := newStandingRig()
	voidedAt := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	r.store.voided = &standing.Voided{At: voidedAt, Reason: "plugin updated"}
	a := standingAdmin{svc: func() *standingService { return r.svc }}
	got, err := a.Paused(context.Background(), "pricer", "prices")
	if err != nil || got == nil || got.Reason != "plugin updated" || !got.At.Equal(voidedAt) {
		t.Fatalf("Paused = %+v, %v", got, err)
	}
}

// An update between showing the rule and allowing it is refused, and nothing
// is recorded.
func TestApproveRefusesAReleaseTheOwnerDidNotRead(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "0.9.0", standingHash); !errors.Is(err, admin.ErrStandingChanged) {
		t.Fatalf("old version: %v", err)
	}
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", "other"); !errors.Is(err, admin.ErrStandingChanged) {
		t.Fatalf("old manifest: %v", err)
	}
	if len(r.store.approved) != 0 {
		t.Fatalf("%d approvals recorded", len(r.store.approved))
	}
}

// The tick starts nothing while a run of the plugin is still waiting for
// review, so a run that keeps being refused never piles up changesets.
func TestCronTickWaitsWhileARunIsWaiting(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	gate := standingGate{r.svc}
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
		t.Fatal(err)
	}
	if on, err := gate.Active(ctx, "pricer", "prices"); err != nil || !on {
		t.Fatalf("nothing waiting: %v %v", on, err)
	}
	r.store.waiting = []standing.WaitingRun{{Run: "prices", ChangesetID: 3, Items: 1}}
	r.eng.verdict = changeset.PolicyVerdict{Reasons: []string{"Change 1 needs a closer look."}}
	before := len(*r.audits)
	if on, err := gate.Active(ctx, "pricer", "prices"); err != nil || on {
		t.Fatalf("a run is waiting: %v %v", on, err)
	}
	if r.store.active == nil {
		t.Fatal("waiting must not end the approval")
	}
	if len(*r.audits) != before {
		t.Fatalf("a refusal seen again must not be audited twice: %v", *r.audits)
	}
}

// A staged changeset the gate never judged (a crash after the run finished, a
// transient error) is offered to the gate again by the tick and does not stall
// the job once it publishes.
func TestCronTickOffersAWaitingRunToTheGateAgain(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	gate := standingGate{r.svc}
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
		t.Fatal(err)
	}
	r.store.waiting = []standing.WaitingRun{{Run: "prices", ChangesetID: 3, Items: 1}}
	if on, err := gate.Active(ctx, "pricer", "prices"); err != nil || !on {
		t.Fatalf("Active = %v, %v; want the tick to go on", on, err)
	}
	if !r.eng.published {
		t.Fatal("the waiting changeset was not offered to the gate")
	}
}

// An approval that no longer matches ends with a reason, so the tab shows the
// paused state and the tick stops asking.
func TestCronTickVoidsAnApprovalThatNoLongerMatches(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(r standingRig)
		reason string
	}{
		"plugin updated": {func(r standingRig) { *r.version = "1.1.0" }, "plugin updated"},
		"schema changed": {func(r standingRig) { *r.schema = `{"type":"object","properties":{"x":{}}}` }, "schema changed"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newStandingRig()
			ctx := context.Background()
			gate := standingGate{r.svc}
			if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
				t.Fatal(err)
			}
			tc.mutate(r)
			if on, err := gate.Active(ctx, "pricer", "prices"); err != nil || on {
				t.Fatalf("Active = %v, %v", on, err)
			}
			if r.store.voided == nil || r.store.voided.Reason != tc.reason || r.store.active != nil {
				t.Fatalf("voided = %+v, active = %v", r.store.voided, r.store.active)
			}
			if last := (*r.audits)[len(*r.audits)-1]; last != "standing.void" {
				t.Fatalf("last audit = %s", last)
			}
		})
	}
}

// Turning off ends the approval with its own reason and is audited, so the
// tab shows the paused state and the tick stops.
func TestTurnOffVoidsWithItsOwnReason(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.TurnOff(ctx, "pricer"); err != nil {
		t.Fatal(err)
	}
	if on, _ := (standingGate{r.svc}).Active(ctx, "pricer", "prices"); on {
		t.Fatal("the tick may still start the run")
	}
	if r.store.voided == nil || r.store.voided.Reason != "turned off" {
		t.Fatalf("voided = %+v", r.store.voided)
	}
	if last := (*r.audits)[len(*r.audits)-1]; last != "standing.void" {
		t.Fatalf("last audit = %s", last)
	}
}

// A second or stale turn off finds nothing active and writes no audit row.
func TestTurnOffAuditsOnlyWhenSomethingWasOn(t *testing.T) {
	r := newStandingRig()
	ctx := context.Background()
	if err := r.svc.Approve(ctx, "pricer", "prices", "owner", "1.0.0", standingHash); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.svc.TurnOff(ctx, "pricer"); err != nil {
			t.Fatal(err)
		}
	}
	voids := 0
	for _, a := range *r.audits {
		if a == "standing.void" {
			voids++
		}
	}
	if voids != 1 {
		t.Fatalf("audits = %v, want one standing.void", *r.audits)
	}
}
