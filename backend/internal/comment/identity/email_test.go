package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/comment"
)

// fakeBlocks lists domains and, like the model, matches their parent domains.
type fakeBlocks struct {
	listed []string
	probes []comment.Probe
	err    error
}

func (f *fakeBlocks) MatchBlocks(_ context.Context, p comment.Probe) ([]comment.Block, error) {
	f.probes = append(f.probes, p)
	if f.err != nil {
		return nil, f.err
	}
	var out []comment.Block
	for _, d := range f.listed {
		if p.EmailDomain == d || strings.HasSuffix(p.EmailDomain, "."+d) {
			out = append(out, comment.Block{Kind: comment.BlockEmailDomain, Value: d})
		}
	}
	return out, nil
}

func TestCheckEmail_DisposableNeutralMessage(t *testing.T) {
	t.Parallel()
	f := &fakeBlocks{listed: []string{"mailinator.test"}}
	for _, addr := range []string{"a@mailinator.test", "a@x.mailinator.test", "A@Mailinator.TEST"} {
		if err := checkEmail(context.Background(), f, addr); !errors.Is(err, ErrEmailNotAccepted) {
			t.Errorf("%s: err = %v, want ErrEmailNotAccepted", addr, err)
		}
	}
	if got := f.probes[len(f.probes)-1].EmailDomain; got != "mailinator.test" {
		t.Errorf("probe domain = %q, want lowercased", got)
	}
	for _, bad := range []string{"disposable", "spam", "mailinator"} {
		if strings.Contains(strings.ToLower(MsgEmailNotAccepted), bad) {
			t.Errorf("MsgEmailNotAccepted mentions %q", bad)
		}
	}
}

func TestCheckEmail_Invalid(t *testing.T) {
	t.Parallel()
	f := &fakeBlocks{}
	long := strings.Repeat("a", 250) + "@b.test" // 257 bytes
	for _, addr := range []string{"", "a", "a@b@c", "Ann <a@example.test>", "a@example.test\r\nBcc:x", long, "a@nodot", "@example.test"} {
		if err := checkEmail(context.Background(), f, addr); !errors.Is(err, ErrEmailInvalid) {
			t.Errorf("%q: err = %v, want ErrEmailInvalid", addr, err)
		}
	}
	if len(f.probes) != 0 {
		t.Errorf("MatchBlocks called %d times for invalid input", len(f.probes))
	}
}

func TestCheckEmail_OK(t *testing.T) {
	t.Parallel()
	f := &fakeBlocks{listed: []string{"mailinator.test"}}
	if err := checkEmail(context.Background(), f, "Ann@Example.TEST"); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestCheckEmail_LookupErrorIsNotRefusal(t *testing.T) {
	t.Parallel()
	f := &fakeBlocks{err: errors.New("db down")}
	err := checkEmail(context.Background(), f, "a@example.test")
	if err == nil || errors.Is(err, ErrEmailNotAccepted) || errors.Is(err, ErrEmailInvalid) {
		t.Errorf("err = %v, want a plain lookup error", err)
	}
}
