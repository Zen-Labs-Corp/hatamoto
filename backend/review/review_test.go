package review_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/review"
)

type fake struct {
	stages []review.Stage
	err    error
}

func (f *fake) Stages(context.Context, review.Edit) ([]review.Stage, error) { return f.stages, f.err }

type spy struct{ got review.Edit }

func (s *spy) Stages(_ context.Context, e review.Edit) ([]review.Stage, error) {
	s.got = e
	return []review.Stage{{Name: "Own", Group: "g"}}, nil
}

func reset(t *testing.T) {
	t.Helper()
	review.ResetForTest()
	t.Cleanup(review.ResetForTest)
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	fn()
}

func TestDefaultReviewOff(t *testing.T) {
	reset(t)
	st, err := review.StagesFor(context.Background(), review.Edit{})
	if err != nil || st == nil || len(st) != 0 {
		t.Fatalf("got %#v, %v; want empty slice, nil", st, err)
	}
}

func TestDefaultReviewOn(t *testing.T) {
	reset(t)
	st, err := review.StagesFor(context.Background(), review.Edit{ReviewEnabled: true})
	want := review.Stage{Name: "Review", Group: "reviewers"}
	if err != nil || len(st) != 1 || st[0] != want {
		t.Fatalf("got %#v, %v; want [%v]", st, err, want)
	}
}

func TestRegisteredPolicyHonoured(t *testing.T) {
	reset(t)
	want := []review.Stage{{Name: "A", Group: "g1"}, {Name: "B", Group: "g2"}, {Name: "C", Group: "g3"}}
	review.Register(&fake{stages: want})
	got, err := review.StagesFor(context.Background(), review.Edit{})
	if err != nil || len(got) != 3 {
		t.Fatalf("got %#v, %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stage %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestPolicyErrorFailsClosed(t *testing.T) {
	reset(t)
	review.Register(&fake{stages: []review.Stage{{Name: "A", Group: "g"}}, err: errors.New("boom")})
	st, err := review.StagesFor(context.Background(), review.Edit{ReviewEnabled: true})
	if !errors.Is(err, review.ErrPolicy) || st != nil {
		t.Fatalf("got %#v, %v; want nil, ErrPolicy", st, err)
	}
}

func TestInvalidStageFailsClosed(t *testing.T) {
	for name, s := range map[string]review.Stage{
		"empty name":  {Name: "", Group: "g"},
		"empty group": {Name: "A", Group: " "},
	} {
		t.Run(name, func(t *testing.T) {
			reset(t)
			review.Register(&fake{stages: []review.Stage{s}})
			st, err := review.StagesFor(context.Background(), review.Edit{})
			if !errors.Is(err, review.ErrPolicy) || st != nil {
				t.Fatalf("got %#v, %v; want nil, ErrPolicy", st, err)
			}
		})
	}
}

func TestRegisterPanics(t *testing.T) {
	reset(t)
	review.Register(&fake{})
	mustPanic(t, "second Register", func() { review.Register(&fake{}) })

	reset(t)
	mustPanic(t, "nil", func() { review.Register(nil) })
	mustPanic(t, "typed nil", func() { review.Register((*fake)(nil)) })

	reset(t)
	review.FreezeForTest()
	mustPanic(t, "after Freeze", func() { review.Register(&fake{}) })
}

func TestPolicySeesRoleAndBypassAndWins(t *testing.T) {
	reset(t)
	sp := &spy{}
	review.Register(sp)
	st, err := review.StagesFor(context.Background(), review.Edit{ReviewEnabled: true, RequesterRole: "owner", Bypass: true})
	if err != nil || len(st) != 1 || st[0].Name != "Own" {
		t.Fatalf("got %#v, %v; the policy's stage must win over Bypass", st, err)
	}
	if sp.got.RequesterRole != "owner" || !sp.got.Bypass {
		t.Fatalf("policy saw %#v", sp.got)
	}
}

type resolver struct {
	ok    bool
	err   error
	panic bool
}

func (r *resolver) MayDecide(context.Context, string, string, string, string) (bool, error) {
	if r.panic {
		panic("boom")
	}
	return r.ok, r.err
}

func TestRegisterGroupsPanics(t *testing.T) {
	reset(t)
	review.RegisterGroups(&resolver{})
	mustPanic(t, "second RegisterGroups", func() { review.RegisterGroups(&resolver{}) })

	reset(t)
	mustPanic(t, "nil", func() { review.RegisterGroups(nil) })
	mustPanic(t, "typed nil", func() { review.RegisterGroups((*resolver)(nil)) })

	reset(t)
	review.FreezeForTest()
	mustPanic(t, "after Freeze", func() { review.RegisterGroups(&resolver{}) })
}

func TestGroupResolverRegistersAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		r    *resolver
		want bool
		err  bool
	}{
		{"yes", &resolver{ok: true}, true, false},
		{"no", &resolver{}, false, false},
		{"error", &resolver{ok: true, err: errors.New("down")}, false, true},
		{"panic", &resolver{ok: true, panic: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset(t)
			review.RegisterGroups(tc.r)
			got, err := review.MayDecideForTest(ctx, "u", "editor", "legal", "e")
			if got != tc.want || (err != nil) != tc.err {
				t.Fatalf("got %v, %v; want %v, err=%v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestPendingUnavailableOutsideServe(t *testing.T) {
	reset(t)
	if _, err := review.Pending(context.Background()); !errors.Is(err, review.ErrUnavailable) {
		t.Fatalf("got %v; want ErrUnavailable", err)
	}
	review.SetPendingSourceForTest(func(context.Context, int) ([]review.PendingStage, error) {
		return []review.PendingStage{{ChangesetID: 3, Index: 0, Total: 2}}, nil
	})
	rows, err := review.Pending(context.Background())
	if err != nil || len(rows) != 1 || rows[0].ChangesetID != 3 {
		t.Fatalf("got %#v, %v", rows, err)
	}
}

func TestEditCarriesLocaleAndSection(t *testing.T) {
	reset(t)
	sp := &spy{}
	review.Register(sp)
	if _, err := review.StagesFor(context.Background(), review.Edit{ReviewEnabled: true, Locale: "fr", Section: "/pricing"}); err != nil {
		t.Fatal(err)
	}
	if sp.got.Locale != "fr" || sp.got.Section != "/pricing" {
		t.Fatalf("policy saw %#v", sp.got)
	}
}
