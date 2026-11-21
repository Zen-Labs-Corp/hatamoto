package auditlog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditstate"
)

func TestRedact(t *testing.T) {
	in := map[string]any{"Password": "x", "TOKEN": "y", "api_key": "z", "client_secret": "s", "name": "ok", "n": 1}
	got := Redact(in)
	for _, k := range []string{"Password", "TOKEN", "api_key", "client_secret"} {
		if got[k] != redacted {
			t.Errorf("%s = %v, want redacted", k, got[k])
		}
	}
	if got["name"] != "ok" || got["n"] != 1 {
		t.Errorf("non-secret keys changed: %v", got)
	}
	if in["Password"] != "x" {
		t.Error("input map mutated")
	}
	if Redact(nil) != nil {
		t.Error("nil detail must stay nil")
	}
}

func TestRedactDenylistKeys(t *testing.T) {
	for _, k := range []string{"password", "passwd", "secret", "token", "access_token", "refresh_token",
		"api_key", "apikey", "authorization", "cookie", "private_key", "client_secret"} {
		if Redact(map[string]any{k: "v"})[k] != redacted {
			t.Errorf("%s not redacted", k)
		}
	}
}

func TestKindFor(t *testing.T) {
	const id = "0b0c5b7e-5d7e-4a53-8b8c-2f1d6f0b9a11"
	for _, c := range []struct{ id, actor, want string }{
		{"", "cli", "cli"}, {"", "api", "api_key"}, {id, "a@example.com", "user"},
		{"", "system", "system"}, {"not-a-uuid", "x", "system"},
	} {
		if got := KindFor(c.id, c.actor); got != c.want {
			t.Errorf("KindFor(%q,%q) = %q, want %q", c.id, c.actor, got, c.want)
		}
	}
}

func TestClamp(t *testing.T) {
	ev := clamp(auditstate.Event{UserAgent: strings.Repeat("é", 400), RequestID: strings.Repeat("r", 300)})
	if len(ev.UserAgent) > maxUserAgent || len(ev.RequestID) != maxRequestID {
		t.Errorf("lens = %d, %d", len(ev.UserAgent), len(ev.RequestID))
	}
	if ev.Result != "ok" {
		t.Errorf("Result = %q", ev.Result)
	}
}

func TestMustFail(t *testing.T) {
	if !MustFail(fmt.Errorf("%w: boom", ErrSink)) || MustFail(fmt.Errorf("%w: boom", ErrTable)) || MustFail(errors.New("x")) {
		t.Error("MustFail mismatch")
	}
}

type ctxSink struct{ err *error }

func (c ctxSink) Record(ctx context.Context, _ pgx.Tx, _ auditstate.Event) error {
	*c.err = ctx.Err()
	return nil
}

// With no tx the change is already committed, so a cancelled request ctx must
// not reach the sink.
func TestRecordNoTxSinkIgnoresCancellation(t *testing.T) {
	var got error
	auditstate.Register(ctxSink{&got})
	t.Cleanup(auditstate.Reset)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Recorder{}).Record(ctx, nil, auditstate.Event{Action: "x"}); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("sink ctx err = %v, want nil", got)
	}
}
