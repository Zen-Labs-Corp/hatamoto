package sdk

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

func TestValidIdentityAccount(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"one byte", "a", true},
		{"128 bytes", strings.Repeat("a", 128), true},
		{"129 bytes", strings.Repeat("a", 129), false},
		{"control", "\x01", false},
		{"newline", "a\nb", false},
		{"invalid utf-8", "\xff\xfe", false},
		{"unicode", "名前", true},
	} {
		if got := ValidIdentityAccount(c.in); got != c.want {
			t.Errorf("%s: ValidIdentityAccount = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCommenterSessionValid(t *testing.T) {
	for _, c := range []struct {
		name string
		s    CommenterSession
		ok   bool
	}{
		{"sign in", CommenterSession{Account: "u1"}, true},
		{"sign in with link and max_age", CommenterSession{Account: "u1", Link: true, MaxAge: 60}, true},
		{"sign out alone", CommenterSession{SignOut: true}, true},
		{"sign out with account", CommenterSession{SignOut: true, Account: "u1"}, false},
		{"sign out with link", CommenterSession{SignOut: true, Link: true}, false},
		{"sign out with max_age", CommenterSession{SignOut: true, MaxAge: 5}, false},
		{"link without account", CommenterSession{Link: true}, false},
		{"empty", CommenterSession{}, false},
		{"negative max_age", CommenterSession{Account: "u1", MaxAge: -1}, false},
		{"bad account", CommenterSession{Account: "a\x00"}, false},
	} {
		if err := c.s.Valid(); (err == nil) != c.ok {
			t.Errorf("%s: Valid() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestHTTPResponseCommenterSessionRoundTrip(t *testing.T) {
	in := HTTPResponse{Status: 200, CommenterSession: &CommenterSession{Account: "u1", Link: true, MaxAge: 30}}
	b, err := jsonv2.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out HTTPResponse
	if err := jsonv2.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.CommenterSession == nil || *out.CommenterSession != *in.CommenterSession {
		t.Fatalf("round trip = %+v from %s", out.CommenterSession, b)
	}
	b, err = jsonv2.Marshal(HTTPResponse{Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "commenter_session") {
		t.Errorf("nil session is serialised: %s", b)
	}
}

func TestIdentityHostFunctionNames(t *testing.T) {
	if FnCommentsIdentityEnsure != "comments.identity.ensure" || FnCommentsIdentityRevoke != "comments.identity.revoke" {
		t.Errorf("names: %q %q", FnCommentsIdentityEnsure, FnCommentsIdentityRevoke)
	}
}
