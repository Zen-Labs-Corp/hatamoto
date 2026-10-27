package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
)

func TestParseArgsAdminURL(t *testing.T) {
	cases := []struct {
		args []string
		want command
		err  bool
	}{
		{[]string{"admin-url"}, cmdAdminURL, false},
		{[]string{"admin-url", "set", "/my-place"}, cmdAdminURL, false},
		{[]string{"admin-url", "set", "/admin", "--i-understand-weak"}, cmdAdminURL, false},
		{[]string{"admin-url", "--i-understand-weak", "set", "/admin"}, cmdAdminURL, false},
		{[]string{"admin-url", "random"}, cmdAdminURL, false},
		{[]string{"admin-url", "set"}, cmdHelp, true},
		{[]string{"admin-url", "set", "/a", "/b"}, cmdHelp, true},
		{[]string{"admin-url", "random", "--i-understand-weak"}, cmdHelp, true},
		{[]string{"admin-url", "--bogus"}, cmdHelp, true},
		{[]string{"admin-url", "frob"}, cmdHelp, true},
		{[]string{"admin-url", "--i-understand-weak"}, cmdHelp, true},
	}
	for _, c := range cases {
		got, err := parseArgs(c.args)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("parseArgs(%q) = %v, %v; want %v, err=%v", c.args, got, err, c.want, c.err)
		}
	}
}

type fakeSite struct{}

func (fakeSite) Serves(context.Context, string) (bool, string) { return false, "" }
func (fakeSite) Name() string                                  { return "" }
func (fakeSite) Domain() string                                { return "" }

func noEnv(string) string { return "" }

func baseOf(s string) func(context.Context) string { return func(context.Context) string { return s } }

func runURL(t *testing.T, st adminStore, env func(string) string, base string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runAdminURL(context.Background(), st, fakeSite{}, args, env, baseOf(base), &out)
	return out.String(), err
}

func TestAdminURLShow(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	out, err := runURL(t, st, noEnv, "https://example.com/")
	if err != nil || out != "https://example.com/k7qm-x2rf-9tdc\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestSetWeakRefusedNoWrite(t *testing.T) {
	st := &fakeAdminStore{}
	out, err := runURL(t, st, noEnv, "", "set", "/admin")
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	want := "refused: \"/admin\" is weak (on the scanner list). Re-run with --i-understand-weak to use it anyway.\n"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if len(st.sets)+len(st.acks) != 0 || st.cur.Path != nil {
		t.Error("store was written")
	}
}

func TestSetWeakFlagBeforeAndAfter(t *testing.T) {
	for _, args := range [][]string{
		{"set", "/admin", weakFlag},
		{weakFlag, "set", "/admin"},
		{"set", weakFlag, "/admin"},
	} {
		st := &fakeAdminStore{}
		out, err := runURL(t, st, noEnv, "https://example.com", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "admin address set to /admin (weak, acknowledged by cli at 20") ||
			lines[1] != "https://example.com/admin" || lines[2] != "restart hatamoto to apply" {
			t.Errorf("%v: out = %q", args, out)
		}
		if len(st.acks) != 1 || st.cur.AckBy == nil || *st.cur.AckBy != "cli" || st.cur.AckAt == nil {
			t.Errorf("%v: ack not recorded: %+v", args, st.cur)
		}
		if st.cur.SetBy == nil || *st.cur.SetBy != "cli" {
			t.Errorf("%v: set by = %v, want cli", args, st.cur.SetBy)
		}
	}
}

func TestSetStrongNoAckWithFlag(t *testing.T) {
	st := &fakeAdminStore{}
	out, err := runURL(t, st, noEnv, "", "set", "/n3wq-8vhd-p2ka", weakFlag)
	if err != nil {
		t.Fatal(err)
	}
	if out != "admin address set to /n3wq-8vhd-p2ka (strong)\n/n3wq-8vhd-p2ka\nrestart hatamoto to apply\n" {
		t.Errorf("out = %q", out)
	}
	if len(st.acks) != 0 || st.cur.AckBy != nil || len(st.sets) != 1 {
		t.Errorf("no ack expected: %+v acks=%v", st.cur, st.acks)
	}
	if st.cur.SetBy == nil || *st.cur.SetBy != "cli" {
		t.Errorf("set by = %v, want cli", st.cur.SetBy)
	}
}

func TestSetReservedRefusedEvenWithFlag(t *testing.T) {
	for _, args := range [][]string{{"set", "/api"}, {"set", "/api", weakFlag}} {
		st := &fakeAdminStore{}
		out, err := runURL(t, st, noEnv, "", args...)
		var ee exitError
		if !errors.As(err, &ee) || ee.code != 1 {
			t.Fatalf("err = %v", err)
		}
		if out != "refused: \"/api\" is reserved (the site serves /api).\n" {
			t.Errorf("out = %q", out)
		}
		if st.cur.Path != nil {
			t.Error("store was written")
		}
	}
}

func TestSetInvalidRefusedEvenWithFlag(t *testing.T) {
	st := &fakeAdminStore{}
	out, err := runURL(t, st, noEnv, "", "set", "/a/b", weakFlag)
	var ee exitError
	if !errors.As(err, &ee) || !strings.HasPrefix(out, "refused: ") || st.cur.Path != nil {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestRandomStrongClearsAck(t *testing.T) {
	st := &fakeAdminStore{}
	if err := st.SetAck(context.Background(), "/admin", "cli"); err != nil {
		t.Fatal(err)
	}
	out, err := runURL(t, st, noEnv, "https://example.com", "random")
	if err != nil {
		t.Fatal(err)
	}
	if st.cur.AckBy != nil || st.cur.AckAt != nil || st.cur.Path == nil {
		t.Fatalf("store = %+v", st.cur)
	}
	if st.cur.SetBy == nil || *st.cur.SetBy != "cli" {
		t.Errorf("set by = %v, want cli", st.cur.SetBy)
	}
	if res := adminpath.Assess(context.Background(), *st.cur.Path, nil); res.Level != adminpath.LevelStrong {
		t.Errorf("%q is %v", *st.cur.Path, res.Level)
	}
	want := "admin address set to " + *st.cur.Path + " (strong)\nhttps://example.com" + *st.cur.Path + "\nrestart hatamoto to apply\n"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestEnvPinNotice(t *testing.T) {
	env := func(k string) string {
		if k == "HATAMOTO_ADMIN_PATH" {
			return "/pinned-place-x7q2"
		}
		return ""
	}
	for _, args := range [][]string{nil, {"set", "/n3wq-8vhd-p2ka"}, {"random"}} {
		st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
		out, err := runURL(t, st, env, "https://example.com", args...)
		stored := *st.cur.Path
		note := "note: HATAMOTO_ADMIN_PATH pins the served address to /pinned-place-x7q2; the stored value is " + stored + " and is not what is served"
		if err != nil || !strings.Contains(out, note) {
			t.Errorf("%v: out = %q, err = %v", args, out, err)
		}
		if !strings.Contains("\n"+out, "\nhttps://example.com/pinned-place-x7q2\n") || strings.Contains(out, "example.com"+stored) {
			t.Errorf("%v: the URL line must be the pinned address: %q", args, out)
		}
	}
	out, _ := runURL(t, &fakeAdminStore{}, noEnv, "", "random")
	if strings.Contains(out, "note:") {
		t.Errorf("unexpected note: %q", out)
	}
}

func TestBaseURLOrder(t *testing.T) {
	st := &fakeAdminStore{cur: adminpath.Stored{Path: strp("/k7qm-x2rf-9tdc")}}
	for base, want := range map[string]string{
		"https://env.example/": "https://env.example/k7qm-x2rf-9tdc\n",
		"https://site.example": "https://site.example/k7qm-x2rf-9tdc\n",
		"":                     "/k7qm-x2rf-9tdc\n",
	} {
		if out, _ := runURL(t, st, noEnv, base); out != want {
			t.Errorf("base %q: out = %q, want %q", base, out, want)
		}
	}
}

func TestNoEmEnDash(t *testing.T) {
	var all strings.Builder
	st := &fakeAdminStore{}
	for _, args := range [][]string{{"set", "/admin"}, {"set", "/api"}, {"set", "/a/b"}, {"set", "/admin", weakFlag}, {"random"}, nil} {
		out, _ := runURL(t, st, func(string) string { return "/x-pin-place" }, "", args...)
		all.WriteString(out)
	}
	all.WriteString(usage)
	if strings.ContainsAny(all.String(), "\u2013\u2014") {
		t.Error("output contains an em or en dash")
	}
}
