package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testBackupRecipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
	testBackupPrivate   = "AGE-SECRET-" + "KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
	testBackupPass      = "correct horse battery staple"
)

// fakeBackupKey is a BackupKeyService that records its calls.
type fakeBackupKey struct {
	view     BackupKeyView
	viewErr  error
	pubErr   error
	passErr  error
	pubs     []string
	passes   []string
	cleared  int
	callsAll int
}

func (f *fakeBackupKey) View(context.Context) (BackupKeyView, error) { return f.view, f.viewErr }
func (f *fakeBackupKey) SavePublicKey(_ context.Context, s string) error {
	f.callsAll++
	f.pubs = append(f.pubs, s)
	return f.pubErr
}
func (f *fakeBackupKey) SavePassphrase(_ context.Context, p string) error {
	f.callsAll++
	f.passes = append(f.passes, p)
	return f.passErr
}
func (f *fakeBackupKey) Clear(context.Context) error { f.callsAll++; f.cleared++; return nil }

func newBackupKeyRig(t *testing.T) (*backupsRig, *fakeBackupKey) {
	t.Helper()
	g := newBackupsRig(t, baseBackups())
	key := &fakeBackupKey{}
	g.a.SetBackupKey(key)
	return g, key
}

const keyPath = backupsPath + "/encryption"

func TestBackupKeySaveNeedsSudo(t *testing.T) {
	t.Parallel()

	g, key := newBackupKeyRig(t)
	form := url.Values{"mode": {"public"}, "public_key": {testBackupRecipient}}
	rec := g.do("POST", keyPath, "owner-token", form, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), g.a.sudoPath()) || key.callsAll != 0 || len(g.events) != 0 {
		t.Fatalf("without sudo = %d %s, calls %d, audit %+v", rec.Code, rec.Header().Get("Location"), key.callsAll, g.events)
	}
	rec = g.do("POST", keyPath+"/off", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), g.a.sudoPath()) || key.callsAll != 0 {
		t.Fatalf("off without sudo = %d %s, calls %d", rec.Code, rec.Header().Get("Location"), key.callsAll)
	}
}

func TestBackupKeySaveStoresAndAuditsOnlyTheMode(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		form url.Values
		mode string
	}{
		{"generated key", url.Values{"mode": {"generate"}, "public_key": {testBackupRecipient}}, "key"},
		{"pasted key", url.Values{"mode": {"public"}, "public_key": {" " + testBackupRecipient + " "}}, "key"},
		{"passphrase", url.Values{"mode": {"passphrase"}, "passphrase": {testBackupPass}}, "passphrase"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, key := newBackupKeyRig(t)
			grantSudo(g.ownerRig.rig)
			rec := g.do("POST", keyPath, "owner-token", c.form, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != backupsPath+"#backupkey" {
				t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
			}
			if c.mode == "key" && (len(key.pubs) != 1 || key.pubs[0] != testBackupRecipient) {
				t.Fatalf("saved keys %q", key.pubs)
			}
			if c.mode == "passphrase" && (len(key.passes) != 1 || key.passes[0] != testBackupPass) {
				t.Fatalf("saved passphrases %q", key.passes)
			}
			if len(g.events) != 1 || g.events[0].Action != "backups.encryption.update" || len(g.events[0].Detail) != 1 || g.events[0].Detail["mode"] != c.mode {
				t.Fatalf("audit = %+v", g.events)
			}
			if all := fmt.Sprint(g.events); strings.Contains(all, testBackupRecipient) || strings.Contains(all, testBackupPass) {
				t.Fatalf("audit holds a value: %s", all)
			}
		})
	}
}

func TestBackupKeyRefusals(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		form   url.Values
		set    func(*fakeBackupKey)
		absent []string
	}{
		{"private key", url.Values{"mode": {"public"}, "public_key": {testBackupPrivate}},
			func(f *fakeBackupKey) { f.pubErr = ErrBackupPrivateKey }, []string{testBackupPrivate}},
		{"invalid key", url.Values{"mode": {"public"}, "public_key": {"nonsense"}},
			func(f *fakeBackupKey) { f.pubErr = ErrBackupKeyInvalid }, nil},
		{"short passphrase", url.Values{"mode": {"passphrase"}, "passphrase": {"short pass"}},
			func(f *fakeBackupKey) { f.passErr = ErrBackupPassphraseShort }, []string{"short pass"}},
		{"generate without a key", url.Values{"mode": {"generate"}},
			nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, key := newBackupKeyRig(t)
			if c.set != nil {
				c.set(key)
			}
			grantSudo(g.ownerRig.rig)
			rec := g.do("POST", keyPath, "owner-token", c.form, nil)
			body := rec.Body.String()
			if rec.Code != 422 {
				t.Fatalf("save = %d, want 422", rec.Code)
			}
			for _, s := range c.absent {
				if strings.Contains(body, s) {
					t.Fatalf("the page echoes %q", s)
				}
			}
			if len(g.events) != 0 {
				t.Fatalf("a refused save was audited: %+v", g.events)
			}
		})
	}
}

func TestBackupKeyOff(t *testing.T) {
	t.Parallel()

	g, key := newBackupKeyRig(t)
	grantSudo(g.ownerRig.rig)
	rec := g.do("POST", keyPath+"/off", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || key.cleared != 1 {
		t.Fatalf("off = %d, cleared %d", rec.Code, key.cleared)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.encryption.off" || len(g.events[0].Detail) != 0 {
		t.Fatalf("audit = %+v", g.events)
	}
}

func TestBackupKeyServiceFailureIsNotAServerError(t *testing.T) {
	t.Parallel()

	g, key := newBackupKeyRig(t)
	key.viewErr = errors.New("database down")
	if rec := g.do("GET", backupsPath, "owner-token", nil, nil); rec.Code != 200 {
		t.Fatalf("page with a failing key view = %d, want 200", rec.Code)
	}
}

func TestBackupKeyCardCountsPlainOffsiteCopies(t *testing.T) {
	t.Parallel()

	g, key := newBackupKeyRig(t)
	key.view = BackupKeyView{Mode: BackupKeyPublic, PublicKey: testBackupRecipient}
	rows := backupRows()
	rows[1].Offsite = "uploaded"
	rows[2].Offsite, rows[2].OffsiteEncrypted = "uploaded", true
	d := g.a.backupKeyCard(httptest.NewRequest("GET", backupsPath, nil), BackupsState{Rows: rows}, nil)
	if d == nil || d.PlainOffsite != 1 || d.Selected != "public" || d.Status.Label != "On: public key set" {
		t.Fatalf("card = %+v", d)
	}
	key.view = BackupKeyView{}
	if d := g.a.backupKeyCard(httptest.NewRequest("GET", backupsPath, nil), BackupsState{Rows: rows}, nil); d.PlainOffsite != 0 || d.Selected != "generate" {
		t.Fatalf("off card = %+v", d)
	}
}
