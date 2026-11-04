package twofactor

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testOrigin = "https://example.com"

type pkRig struct {
	*rig
	pk *Passkeys
	va *virtualAuthenticator
}

func newPKRig(t *testing.T) *pkRig {
	t.Helper()
	r := newRig(t)
	rp := func(context.Context) (RP, error) {
		return RP{ID: "example.com", Name: "Example", Origins: []string{testOrigin, "https://admin.example.com"}}, nil
	}
	return &pkRig{rig: r, pk: NewPasskeys(rp, r.repo, r.svc, func() time.Time { return r.now }), va: newVirtualAuthenticator(t)}
}

// register adds the virtual authenticator as a passkey of r.uid.
func (r *pkRig) register(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	opts, cid, err := r.pk.BeginRegistration(ctx, r.uid, "ana@example.com", "Ana")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pk.FinishRegistration(ctx, cid, r.uid, "  Laptop ", r.va.register(opts, testOrigin)); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestRegistrationOptionsAskForUserVerification(t *testing.T) {
	r := newPKRig(t)
	opts, _, err := r.pk.BeginRegistration(context.Background(), r.uid, "ana@example.com", "Ana")
	if err != nil {
		t.Fatal(err)
	}
	s := string(opts)
	for _, want := range []string{`"userVerification":"required"`, `"residentKey":"required"`, `"attestation":"none"`, `"id":"example.com"`} {
		if !strings.Contains(s, want) {
			t.Errorf("options lack %s: %s", want, s)
		}
	}
}

func TestRegisterAndSecondFactorLogin(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	r.register(t)
	list, _ := r.repo.ListPasskeys(ctx, r.uid)
	if len(list) != 1 || list[0].Name != "Laptop" {
		t.Fatalf("passkeys = %+v", list)
	}
	if has, _ := r.svc.HasAnyFactor(ctx, r.uid); !has {
		t.Fatal("a passkey must count as a factor")
	}

	opts, cid, err := r.pk.BeginLogin(ctx, &r.uid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, assertOptions{Origin: testOrigin}))
	if err != nil || got != r.uid {
		t.Fatalf("login = %v %v", got, err)
	}
	list, _ = r.repo.ListPasskeys(ctx, r.uid)
	if list[0].LastUsedAt == nil {
		t.Error("last_used_at not recorded")
	}
}

func TestDiscoverableLogin(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	r.register(t)
	opts, cid, err := r.pk.BeginLogin(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.pk.FinishLogin(ctx, cid, nil, r.va.assert(opts, assertOptions{Origin: testOrigin, UserHandle: true}))
	if err != nil || got != r.uid {
		t.Fatalf("discoverable login = %v %v", got, err)
	}
}

func TestDiscoverableLoginRefusals(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		prep func(r *pkRig)
		opt  assertOptions
	}{
		"no user handle":   {opt: assertOptions{Origin: testOrigin}},
		"wrong origin":     {opt: assertOptions{Origin: "https://evil.example", UserHandle: true}},
		"no user verified": {prep: func(r *pkRig) { r.va.verified = false }, opt: assertOptions{Origin: testOrigin, UserHandle: true}},
		"foreign handle": {prep: func(r *pkRig) {
			other := uuid.New()
			r.va.userID = other[:]
		}, opt: assertOptions{Origin: testOrigin, UserHandle: true}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newPKRig(t)
			r.register(t)
			if tc.prep != nil {
				tc.prep(r)
			}
			opts, cid, err := r.pk.BeginLogin(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.pk.FinishLogin(ctx, cid, nil, r.va.assert(opts, tc.opt)); !errors.Is(err, ErrPasskeyFailed) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestReplayedAssertionRefused(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	r.register(t)
	opts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
	req := r.va.assert(opts, assertOptions{Origin: testOrigin})
	body := new(strings.Builder)
	_, _ = bodyCopy(body, req)
	if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, newBodyReq(body.String())); err != nil {
		t.Fatal(err)
	}
	// Same ceremony id and same bytes again: the challenge is spent.
	if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, newBodyReq(body.String())); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("replay = %v", err)
	}
}

func TestSecondFactorRefusals(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		prep func(r *pkRig)
		opt  assertOptions
	}{
		"wrong origin":     {opt: assertOptions{Origin: "https://evil.example"}},
		"no user verified": {prep: func(r *pkRig) { r.va.verified = false }, opt: assertOptions{Origin: testOrigin}},
		"counter not increasing": {prep: func(r *pkRig) {
			// One good login first so the stored counter is 1, then replay counter 1.
			opts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
			if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, assertOptions{Origin: testOrigin})); err != nil {
				panic(err)
			}
		}, opt: assertOptions{Origin: testOrigin, SetCounter: ptr(uint32(1))}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newPKRig(t)
			r.register(t)
			if tc.prep != nil {
				tc.prep(r)
			}
			opts, cid, err := r.pk.BeginLogin(ctx, &r.uid)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, tc.opt)); !errors.Is(err, ErrPasskeyFailed) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCeremonyBoundToItsUserAndPurpose(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	r.register(t)
	other := uuid.New()
	opts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
	if _, err := r.pk.FinishLogin(ctx, cid, &other, r.va.assert(opts, assertOptions{Origin: testOrigin})); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("another user's ceremony = %v", err)
	}
	// A discoverable ceremony cannot be finished as a second factor and back.
	opts, cid, _ = r.pk.BeginLogin(ctx, nil)
	if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, assertOptions{Origin: testOrigin})); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("mode swap = %v", err)
	}
	// A login ceremony is not a registration ceremony.
	ropts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
	_ = ropts
	if _, err := r.pk.FinishRegistration(ctx, cid, r.uid, "x", r.va.register(mustCreationOptions(t, r), testOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("purpose swap = %v", err)
	}
}

func TestExpiredCeremonyRefused(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	r.register(t)
	opts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
	req := r.va.assert(opts, assertOptions{Origin: testOrigin})
	r.now = r.now.Add(3 * time.Minute)
	if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, req); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("expired = %v", err)
	}
}

func TestSecondFactorFailuresLockAndLockedIsRefused(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	r.register(t)
	for i := 0; i < 5; i++ {
		opts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
		if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, assertOptions{Origin: "https://evil.example"})); !errors.Is(err, ErrPasskeyFailed) {
			t.Fatal(err)
		}
	}
	opts, cid, _ := r.pk.BeginLogin(ctx, &r.uid)
	if _, err := r.pk.FinishLogin(ctx, cid, &r.uid, r.va.assert(opts, assertOptions{Origin: testOrigin})); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("a locked account signed in with a passkey: %v", err)
	}
	// Discoverable sign-in is refused for a locked account too.
	opts, cid, _ = r.pk.BeginLogin(ctx, nil)
	if _, err := r.pk.FinishLogin(ctx, cid, nil, r.va.assert(opts, assertOptions{Origin: testOrigin, UserHandle: true})); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("a locked account signed in discoverably: %v", err)
	}
}

func TestRegistrationRefusals(t *testing.T) {
	ctx := context.Background()
	r := newPKRig(t)
	opts, cid, _ := r.pk.BeginRegistration(ctx, r.uid, "a", "A")
	if _, err := r.pk.FinishRegistration(ctx, cid, r.uid, "x", r.va.register(opts, "https://evil.example")); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("wrong origin = %v", err)
	}
	opts, cid, _ = r.pk.BeginRegistration(ctx, r.uid, "a", "A")
	r.va.verified = false
	if _, err := r.pk.FinishRegistration(ctx, cid, r.uid, "x", r.va.register(opts, testOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("no UV = %v", err)
	}
	r.va.verified = true
	opts, cid, _ = r.pk.BeginRegistration(ctx, r.uid, "a", "A")
	if _, err := r.pk.FinishRegistration(ctx, cid, uuid.New(), "x", r.va.register(opts, testOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("someone else's ceremony = %v", err)
	}
	if n, _ := r.repo.ListPasskeys(ctx, r.uid); len(n) != 0 {
		t.Fatal("a refused registration stored a passkey")
	}
}

func TestNoRelyingParty(t *testing.T) {
	r := newRig(t)
	pk := NewPasskeys(func(context.Context) (RP, error) { return RP{}, nil }, r.repo, r.svc, nil)
	if _, _, err := pk.BeginLogin(context.Background(), nil); !errors.Is(err, ErrNoRelyingParty) {
		t.Fatalf("err = %v", err)
	}
}

func TestCleanPasskeyName(t *testing.T) {
	if CleanPasskeyName("   ") != "Passkey" || CleanPasskeyName(" Phone ") != "Phone" {
		t.Fatal("trim or default")
	}
	if n := len([]rune(CleanPasskeyName(strings.Repeat("é", 200)))); n != 80 {
		t.Fatalf("len = %d", n)
	}
}

func ptr[T any](v T) *T { return &v }

func bodyCopy(dst *strings.Builder, r *http.Request) (int64, error) {
	b := make([]byte, 64<<10)
	n, _ := r.Body.Read(b)
	dst.Write(b[:n])
	return int64(n), nil
}

func newBodyReq(body string) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	return r
}

// mustCreationOptions returns fresh registration options for the rig's user.
func mustCreationOptions(t *testing.T, r *pkRig) []byte {
	t.Helper()
	opts, _, err := r.pk.BeginRegistration(context.Background(), r.uid, "a", "A")
	if err != nil {
		t.Fatal(err)
	}
	return opts
}
