package mailsettings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/email"
)

type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dst ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dst {
		switch p := d.(type) {
		case *string:
			*p = r.vals[i].(string)
		case *int:
			*p = r.vals[i].(int)
		case *[]byte:
			*p, _ = r.vals[i].([]byte)
		case *time.Time:
			*p = r.vals[i].(time.Time)
		}
	}
	return nil
}

type fakeDB struct {
	row      fakeRow
	execSQL  string
	execArgs []any
	execErr  error
}

func (f *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }
func (f *fakeDB) Exec(_ context.Context, sql string, a ...any) (pgconn.CommandTag, error) {
	f.execSQL, f.execArgs = sql, a
	return pgconn.CommandTag{}, f.execErr
}

func valid() Settings {
	return Settings{
		Provider: ProviderCustom, Host: "smtp.example.com", Port: 587,
		Security: email.SecurityStarttls, Username: "u",
		FromAddress: "hello@example.com", FromName: "Example",
	}
}

func TestValidate(t *testing.T) {
	ses := valid()
	ses.Provider, ses.SESRegion, ses.Host = ProviderSES, "eu-west-1", SESHost("eu-west-1")
	gmail := valid()
	gmail.Provider, gmail.Host = ProviderGmail, GmailHost
	local := valid()
	local.Host, local.Security, local.Port = "localhost", email.SecurityNone, 1025

	mut := func(f func(*Settings)) Settings { s := valid(); f(&s); return s }

	cases := []struct {
		name    string
		in      Settings
		wantErr string // substring; empty = valid
	}{
		{"custom", valid(), ""},
		{"ses", ses, ""},
		{"gmail", gmail, ""},
		{"none on localhost", local, ""},
		{"none on 127.0.0.2", mut(func(s *Settings) { s.Host, s.Security = "127.0.0.2", email.SecurityNone }), "localhost"},
		{"none on LOCALHOST", mut(func(s *Settings) { s.Host, s.Security = "LOCALHOST", email.SecurityNone }), "localhost"},
		{"none on ::1", mut(func(s *Settings) { s.Host, s.Security = "::1", email.SecurityNone }), ""},
		{"no credentials", mut(func(s *Settings) { s.Username = "" }), ""},
		{"unknown provider", mut(func(s *Settings) { s.Provider = "postmark" }), "provider"},
		{"unknown region", mut(func(s *Settings) { s.Provider, s.SESRegion = ProviderSES, "mars-1" }), "region"},
		{"ses host mismatch", mut(func(s *Settings) { s.Provider, s.SESRegion = ProviderSES, "eu-west-1" }), "region"},
		{"gmail host mismatch", mut(func(s *Settings) { s.Provider = ProviderGmail }), "Gmail"},
		{"empty host", mut(func(s *Settings) { s.Host = "" }), "host"},
		{"bad host", mut(func(s *Settings) { s.Host = "a b" }), "host"},
		{"port zero", mut(func(s *Settings) { s.Port = 0 }), "port"},
		{"port too big", mut(func(s *Settings) { s.Port = 65536 }), "port"},
		{"unknown security", mut(func(s *Settings) { s.Security = "ssl" }), "security"},
		{"empty security", mut(func(s *Settings) { s.Security = "" }), "security"},
		{"none on public host", mut(func(s *Settings) { s.Security = email.SecurityNone }), "localhost"},
		{"from without at", mut(func(s *Settings) { s.FromAddress = "nope" }), "from address"},
		{"from injection", mut(func(s *Settings) { s.FromAddress = "a@b.c\r\nBcc: x@y.z" }), "from address"},
		{"from name injection", mut(func(s *Settings) { s.FromName = "A\nBcc: x" }), "from name"},
		{"username injection", mut(func(s *Settings) { s.Username = "u\r\n" }), "username"},
		{"from name too long", mut(func(s *Settings) { s.FromName = strings.Repeat("x", MaxFromNameLen+1) }), "from name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestPasswordSealRoundTrip(t *testing.T) {
	key := PasswordKey("server-secret")
	sealed, err := SealPassword(key, "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{PasswordSealed: sealed}
	if !s.HasPassword() {
		t.Fatal("HasPassword = false")
	}
	if got, ok := s.Password(key); !ok || got != "hunter2" {
		t.Fatalf("Password = %q, %v", got, ok)
	}
	if _, ok := s.Password(PasswordKey("rotated")); ok {
		t.Fatal("rotated key must not open the password")
	}
	if _, ok := (Settings{}).Password(key); ok {
		t.Fatal("no sealed password must report ok=false")
	}
	if (Settings{}).HasPassword() {
		t.Fatal("HasPassword on empty = true")
	}
}

func TestGetMissingRow(t *testing.T) {
	st := NewStore(&fakeDB{row: fakeRow{err: pgx.ErrNoRows}})
	_, found, err := st.Get(context.Background())
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestGetMapsRow(t *testing.T) {
	at := time.Unix(1000, 0)
	st := NewStore(&fakeDB{row: fakeRow{vals: []any{
		"ses", "eu-west-1", "email-smtp.eu-west-1.amazonaws.com", 587, "starttls", "AKIA",
		[]byte("sealed"), "a@b.c", "", at,
	}}})
	got, found, err := st.Get(context.Background())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got.Provider != "ses" || got.SESRegion != "eu-west-1" || got.Port != 587 ||
		got.Security != email.SecurityStarttls || got.Username != "AKIA" ||
		string(got.PasswordSealed) != "sealed" || got.FromAddress != "a@b.c" || !got.UpdatedAt.Equal(at) {
		t.Fatalf("got %+v", got)
	}
}

func TestGetError(t *testing.T) {
	st := NewStore(&fakeDB{row: fakeRow{err: errors.New("boom")}})
	if _, found, err := st.Get(context.Background()); err == nil || found {
		t.Fatalf("found=%v err=%v, want error", found, err)
	}
}

func TestSaveValidatesBeforeWriting(t *testing.T) {
	db := &fakeDB{}
	bad := valid()
	bad.Port = 0
	if err := NewStore(db).Save(context.Background(), bad); err == nil {
		t.Fatal("expected validation error")
	}
	if db.execSQL != "" {
		t.Fatal("invalid settings must not reach the database")
	}
}

func TestSaveAndClear(t *testing.T) {
	db := &fakeDB{}
	st := NewStore(db)
	s := valid()
	s.PasswordSealed = []byte("sealed")
	if err := st.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.execSQL, "ON CONFLICT (id)") || len(db.execArgs) != 9 || db.execArgs[3] != 587 || db.execArgs[4] != "starttls" {
		t.Fatalf("sql=%q args=%v", db.execSQL, db.execArgs)
	}
	if err := st.Clear(context.Background()); err != nil || !strings.Contains(db.execSQL, "DELETE FROM hatamoto_smtp_settings") {
		t.Fatalf("clear: err=%v sql=%q", err, db.execSQL)
	}
	db.execErr = errors.New("down")
	if err := st.Clear(context.Background()); err == nil {
		t.Fatal("expected clear error")
	}
}

func TestEmailConfig(t *testing.T) {
	s := valid()
	s.FromName = ""
	c := s.EmailConfig("pw")
	if c.SMTPHost != "smtp.example.com" || c.SMTPPassword != "pw" || !c.AllowLocalhost || !c.FromNameFromSite || c.Security != email.SecurityStarttls {
		t.Fatalf("config = %+v", c)
	}
	s.FromName = "Pinned"
	if c := s.EmailConfig(""); c.FromNameFromSite || c.FromName != "Pinned" {
		t.Fatalf("config = %+v", c)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true,
		"LOCALHOST": false, "localhost.": false, "127.5.5.5": false, "127.0.0.2": false, "[::1]": false,
		"smtp.example.com": false, "10.0.0.1": false, "localhost.evil.com": false, "": false,
	} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
