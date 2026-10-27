package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/adminpath"
)

// assessByName stands in for adminpath.Assess with fixed verdicts.
func assessByName(path string) adminpath.Result {
	res := adminpath.Result{Path: path, Level: adminpath.LevelStrong}
	switch path {
	case "/my-place":
		res.Level = adminpath.LevelWeak
		res.Reasons = []adminpath.Reason{{Code: "scanner", Text: "on the scanner list"}, {Code: "short", Text: "shorter than 10 characters"}}
	case "/api":
		res.Level = adminpath.LevelReserved
		res.Reasons = []adminpath.Reason{{Code: "reserved", Text: "the site serves /api"}}
	case "/a/b":
		res.Level = adminpath.LevelInvalid
		res.Reasons = []adminpath.Reason{{Code: "format", Text: "not a valid admin address: must be a single segment"}}
	}
	return res
}

func addressFormAdmin() *Admin {
	fa := &fakeAddress{assess: assessByName}
	return &Admin{deps: Deps{Address: fa}, adminPath: "/x-test"}
}

func postPicker(vals url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://example.test/save", strings.NewReader(vals.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestAddressFormDecisions(t *testing.T) {
	t.Parallel()

	a := addressFormAdmin()
	cand := "k7qm-x2rf-9tdc"
	for _, c := range []struct {
		name string
		form url.Values
		want addrOutcome
		path string
		ack  bool
	}{
		{"keep strong", url.Values{"mode": {"keep"}, "candidate": {"/" + cand}}, addrOK, "/" + cand, false},
		{"custom strong, no slash, upper case", url.Values{"mode": {"custom"}, "custom": {"  K7QM-x2rf-9tdc "}}, addrOK, "/" + cand, false},
		{"weak, no phrase", url.Values{"mode": {"custom"}, "custom": {"my-place"}}, addrNeedPhrase, "/my-place", false},
		{"weak, wrong phrase", url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {"/my-plac"}}, addrBadPhrase, "/my-place", false},
		{"weak, empty phrase", url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {""}}, addrBadPhrase, "/my-place", false},
		{"weak, exact phrase", url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {"/my-place"}}, addrOK, "/my-place", true},
		{"weak typed in capitals", url.Values{"mode": {"custom"}, "custom": {"My-Place"}, "confirm_phrase": {"/my-place"}}, addrOK, "/my-place", true},
		{"reserved", url.Values{"mode": {"custom"}, "custom": {"api"}}, addrRefused, "/api", false},
		{"invalid", url.Values{"mode": {"custom"}, "custom": {"a/b"}}, addrRefused, "/a/b", false},
	} {
		r := postPicker(c.form)
		f := a.parseAddressForm(r)
		got := a.decideAddress(r, f, "/x-test/save", "/x-test/back")
		if got.Outcome != c.want || got.Path != c.path || got.Ack != c.ack {
			t.Errorf("%s: got outcome %d path %q ack %v, want %d %q %v", c.name, got.Outcome, got.Path, got.Ack, c.want, c.path, c.ack)
		}
		switch c.want {
		case addrBadPhrase:
			if got.Danger.Error != dangerMismatch {
				t.Errorf("%s: card error %q", c.name, got.Danger.Error)
			}
		case addrRefused:
			if got.Message == "" {
				t.Errorf("%s: refusal has no message", c.name)
			}
		}
	}
}

func TestAddressFormCopy(t *testing.T) {
	t.Parallel()

	a := addressFormAdmin()
	r := postPicker(url.Values{"mode": {"custom"}, "custom": {"my-place"}})
	got := a.decideAddress(r, a.parseAddressForm(r), "/x-test/save", "/x-test/back")
	dc := got.Danger
	if dc.Title != "/my-place is the first address attack tools try" ||
		dc.Prompt != "To use this address anyway, type /my-place below." ||
		dc.Label != "I understand, use /my-place" || dc.Phrase != "/my-place" {
		t.Errorf("card copy: %+v", dc)
	}
	var flat strings.Builder
	for _, p := range dc.Body {
		for _, part := range p.Parts {
			flat.WriteString(part.Text)
		}
	}
	for _, want := range []string{
		"Bots scan the whole internet for /admin, /wp-admin and /login every day. On a guessable address they hammer your login page with password guesses and probe for exploits, and they learn which software you run. With a private address, they get a plain \"page not found\".",
		"Your login is still protected by your password, rate limits and two-factor authentication. A private address keeps the bots away from it in the first place.",
	} {
		if !strings.Contains(flat.String(), want) {
			t.Errorf("body lacks %q in %q", want, flat.String())
		}
	}
	all := flat.String() + dc.Title + dc.Prompt + dc.Label
	for _, items := range [][]ui.RadioCardItem{addressItems(addrKeep), addressItems(addrCustom)} {
		for _, it := range items {
			all += it.Title + it.Description
		}
	}
	if strings.ContainsAny(all, "\u2013\u2014") {
		t.Error("copy has an em or en dash")
	}
	if items := addressItems(addrKeep); items[0].Title != "Keep this private address (recommended)" || items[1].Title != "Choose my own" || !items[0].Checked || items[1].Checked {
		t.Errorf("items: %+v", items)
	}
}

func TestAddressFormStrength(t *testing.T) {
	t.Parallel()

	a := addressFormAdmin()
	r := postPicker(url.Values{"mode": {"custom"}, "custom": {"my-place"}})
	f := a.parseAddressForm(r)
	a.evaluateAddress(context.Background(), f)
	if f.Strength == nil || f.Strength.Label != "weak" || f.Strength.Kind != "warning" {
		t.Fatalf("strength: %+v", f.Strength)
	}
	if f.Strength.Reasons[0] != "on the list of addresses attack tools try" || f.Strength.Reasons[1] != "shorter than 10 characters" {
		t.Errorf("reasons: %v", f.Strength.Reasons)
	}
	empty := a.parseAddressForm(postPicker(url.Values{"mode": {"custom"}}))
	a.evaluateAddress(context.Background(), empty)
	if empty.Strength != nil {
		t.Error("an empty custom address has no verdict")
	}
	if got := userLabel(&CurrentUser{Name: " Ana ", Email: "a@b.c"}); got != "Ana" {
		t.Errorf("userLabel name: %q", got)
	}
	if got := userLabel(&CurrentUser{Email: "a@b.c"}); got != "a@b.c" {
		t.Errorf("userLabel email: %q", got)
	}
}
