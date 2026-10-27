package pluginhost

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"hatamoto.dev/sdk"
)

type countingMailer struct {
	mu   sync.Mutex
	sent int // recipients mailed
}

func (m *countingMailer) IsConfigured() bool { return true }

func (m *countingMailer) Send(_ context.Context, to []string, _, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent += len(to)
	return nil
}

func (m *countingMailer) total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent
}

func callMail(t *testing.T, a *API, plugin string, recipients int) sdk.CallResponse {
	t.Helper()
	to := make([]string, recipients)
	for i := range to {
		to[i] = "person" + string(rune('a'+i)) + "@example.test"
	}
	args, _ := json.Marshal(map[string]any{"to": to, "subject": "hello", "text": "body"})
	in, _ := json.Marshal(sdk.CallRequest{Fn: sdk.FnMailSend, Args: args})
	var out sdk.CallResponse
	if err := json.Unmarshal(a.Call(context.Background(), plugin, in), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// mail.send is limited per plugin by recipients, not by calls: a burst of 10
// recipients goes through, the next recipient is refused and nothing is sent
// for it, and another plugin has its own allowance.
func TestMailSendRateLimitPerPlugin(t *testing.T) {
	t.Parallel()
	g := NewGrantStore(nil)
	for _, p := range []string{"noisy", "quiet"} {
		if err := g.Set(context.Background(), p, []sdk.Permission{sdk.PermMailSend}); err != nil {
			t.Fatal(err)
		}
	}
	mailer := &countingMailer{}
	a := NewAPI(Services{Mail: mailer}, g, func(p string) (sdk.Manifest, bool) { return sdk.Manifest{Name: p}, true }, nil)

	if out := callMail(t, a, "noisy", 10); !out.OK {
		t.Fatalf("a burst of 10 recipients was refused: %+v", out.Error)
	}
	if mailer.total() != 10 {
		t.Fatalf("sent %d, want 10", mailer.total())
	}
	out := callMail(t, a, "noisy", 1)
	if out.OK || out.Error == nil || out.Error.Code != sdk.ErrRateLimited {
		t.Fatalf("the 11th recipient within the window must be rate limited, got %+v", out)
	}
	if mailer.total() != 10 {
		t.Fatalf("a refused call sent mail: total %d, want 10", mailer.total())
	}
	if out := callMail(t, a, "quiet", 1); !out.OK {
		t.Fatalf("another plugin shares the limit: %+v", out.Error)
	}
	if mailer.total() != 11 {
		t.Fatalf("sent %d, want 11", mailer.total())
	}
}
