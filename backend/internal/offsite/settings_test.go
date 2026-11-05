package offsite

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func valid() Settings {
	s := Defaults()
	s.Enabled = true
	s.Region, s.Bucket, s.AccessKey = "eu-west-1", "my-backups", "AKEXAMPLE"
	s.SecretSealed = []byte("sealed")
	return s
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Settings){
		"unknown service":     func(s *Settings) { s.Service = "ftp" },
		"empty region":        func(s *Settings) { s.Region = "" },
		"region with slash":   func(s *Settings) { s.Region = "eu/west" },
		"short bucket":        func(s *Settings) { s.Bucket = "ab" },
		"upper bucket":        func(s *Settings) { s.Bucket = "My-Bucket" },
		"bucket underscore":   func(s *Settings) { s.Bucket = "my_bucket" },
		"bucket double dot":   func(s *Settings) { s.Bucket = "my..bucket" },
		"bucket dash edge":    func(s *Settings) { s.Bucket = "-mybucket" },
		"long bucket":         func(s *Settings) { s.Bucket = strings.Repeat("a", 64) },
		"prefix dotdot":       func(s *Settings) { s.Prefix = "a/../b" },
		"prefix slash":        func(s *Settings) { s.Prefix = "/a/" },
		"prefix control":      func(s *Settings) { s.Prefix = "a\nb" },
		"prefix long":         func(s *Settings) { s.Prefix = strings.Repeat("a", 201) },
		"no access key":       func(s *Settings) { s.AccessKey = "" },
		"space in access key": func(s *Settings) { s.AccessKey = "a b" },
		"enabled no secret":   func(s *Settings) { s.SecretSealed = nil },
		"keep zero":           func(s *Settings) { s.Keep = 0 },
		"keep too big":        func(s *Settings) { s.Keep = 366 },
		"bad hour":            func(s *Settings) { s.Schedule = Schedule{ScheduleDaily, 24, 0} },
		"bad weekday":         func(s *Settings) { s.Schedule = Schedule{ScheduleWeekly, 3, 7} },
		"custom no endpoint":  func(s *Settings) { s.Service = ServiceCustom; s.Endpoint = "" },
		"custom http public":  func(s *Settings) { s.Service = ServiceMinIO; s.Endpoint = "http://s3.example.com" },
		"custom userinfo":     func(s *Settings) { s.Service = ServiceCustom; s.Endpoint = "https://u:p@s3.example.com" },
		"custom query":        func(s *Settings) { s.Service = ServiceCustom; s.Endpoint = "https://s3.example.com?x=1" },
		"custom fragment":     func(s *Settings) { s.Service = ServiceCustom; s.Endpoint = "https://s3.example.com#x" },
	} {
		s := valid()
		mut(&s)
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A disabled target may be saved before the secret is entered.
	s := valid()
	s.Enabled, s.SecretSealed = false, nil
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, ep := range []string{"https://acct.r2.cloudflarestorage.com", "http://localhost:9000", "http://10.0.0.5:9000"} {
		c := valid()
		c.Service, c.Endpoint, c.Region = ServiceCustom, ep, "auto"
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", ep, err)
		}
	}
}

func TestAWSDerivesEndpointFromRegion(t *testing.T) {
	s := valid()
	s.Endpoint = "https://ignored.example.com"
	if got := s.EffectiveEndpoint(); got != "https://s3.eu-west-1.amazonaws.com" {
		t.Fatalf("endpoint = %s", got)
	}
	s.Service, s.Endpoint = ServiceR2, "https://x.example.com"
	if got := s.EffectiveEndpoint(); got != "https://x.example.com" {
		t.Fatalf("endpoint = %s", got)
	}
}

func TestSecretSealRoundTrip(t *testing.T) {
	key := SecretKey("server-secret")
	sealed, err := SealSecret(key, "s3-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("s3-secret-value")) {
		t.Fatal("secret stored in clear")
	}
	s := Settings{SecretSealed: sealed}
	if got, ok := s.Secret(key); !ok || got != "s3-secret-value" {
		t.Fatalf("Secret = %q, %v", got, ok)
	}
	if _, ok := s.Secret(SecretKey("another-secret")); ok {
		t.Fatal("opened with the wrong server secret")
	}
	if !s.HasSecret() || (Settings{}).HasSecret() {
		t.Fatal("HasSecret")
	}
	if _, ok := (Settings{}).Secret(key); ok {
		t.Fatal("empty settings opened")
	}
}

type fakeDB struct {
	execs   int
	rowErr  error
	lastSQL string
}

type fakeRow struct{ err error }

func (r fakeRow) Scan(dst ...any) error {
	if r.err == nil && len(dst) == 1 {
		if p, ok := dst[0].(*bool); ok {
			*p = true
		}
	}
	return r.err
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.lastSQL = sql
	return fakeRow{err: f.rowErr}
}
func (f *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.execs++
	return pgconn.CommandTag{}, nil
}

func TestSaveRejectsInvalidBeforeTheDatabase(t *testing.T) {
	db := &fakeDB{}
	bad := valid()
	bad.Bucket = "x"
	if err := NewStore(db).Save(t.Context(), bad); err == nil || db.execs != 0 {
		t.Fatalf("err = %v, execs = %d", err, db.execs)
	}
	if err := NewStore(db).Save(t.Context(), valid()); err != nil || db.execs != 1 {
		t.Fatalf("err = %v, execs = %d", err, db.execs)
	}
}

func TestGetNoRowAndClaim(t *testing.T) {
	db := &fakeDB{rowErr: pgx.ErrNoRows}
	st := NewStore(db)
	if _, found, err := st.Get(t.Context()); found || err != nil {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if ok, err := st.Claim(t.Context(), time.Now(), time.Now()); ok || err != nil {
		t.Fatalf("claim without a due row = %v, %v", ok, err)
	}
	db.rowErr = nil
	if ok, err := st.Claim(t.Context(), time.Now(), time.Now()); !ok || err != nil {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	db.rowErr = errors.New("down")
	if _, err := st.Claim(t.Context(), time.Now(), time.Now()); err == nil {
		t.Fatal("db error swallowed")
	}
}
