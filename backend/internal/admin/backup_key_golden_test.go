package admin

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// privateKeyField is the whole #backupkey-private element: it is filled in the
// browser and must never be a form control that posts.
var privateKeyField = regexp.MustCompile(`<textarea[^>]*id="backupkey-private"[^>]*>`)

func assertPrivateFieldUnnamed(t *testing.T, body string) {
	t.Helper()
	el := privateKeyField.FindString(body)
	if el == "" {
		t.Fatal("page has no #backupkey-private textarea")
	}
	if regexp.MustCompile(`\bname=`).MatchString(el) {
		t.Errorf("the private key textarea has a name, so it would post: %s", el)
	}
}

func TestBackupKeyCardStates(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		view    BackupKeyView
		wants   []string
		absents []string
	}{
		{"key-off", BackupKeyView{Mode: BackupKeyOff}, []string{
			"Backup encryption", ">Off<", "Generate a key pair for me", "I have a public key", "Use a passphrase instead",
			`data-hm-backupkey-generate`, `data-hm-backupkey-reveal`, "The private key is shown once.", "To restore you need the private key",
			`action="/admin/settings/backups/encryption"`, `name="mode"`, `name="public_key"`, `name="passphrase"`,
			`href="/admin/settings/backups/20261018T141900Z-pre_update/download"`,
		}, []string{"Turn off", "Download .tar.age", "before encryption was on"}},
		{"key-set", BackupKeyView{Mode: BackupKeyPublic, PublicKey: testBackupRecipient}, []string{
			">On: public key set<", testBackupRecipient, "Turn off", `form="backupkey-off-form"`, "Download .tar.age",
			"1 off-site copy was uploaded before encryption was on. It stays as it is.",
		}, nil},
		{"key-passphrase", BackupKeyView{Mode: BackupKeyPassphrase}, []string{">On: passphrase<", "Turn off", "Download .tar.age"}, []string{testBackupRecipient}},
		{"key-unreadable", BackupKeyView{Mode: BackupKeyPassphrase, Unreadable: true}, []string{">Key unreadable<", "cannot be read", "Turn off"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := baseBackups()
			f.state.Rows = backupRows()[1:]
			f.state.Rows[0].Offsite = "uploaded"
			g := newBackupsRig(t, f)
			g.a.SetBackupKey(&fakeBackupKey{view: c.view})
			body := g.get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, x := range c.absents {
				if strings.Contains(body, x) {
					t.Errorf("page has %q", x)
				}
			}
			assertPrivateFieldUnnamed(t, body)
			compareGolden(t, "pages/settings-backups-"+c.name+".html", normalizePage(body))
		})
	}
}

func TestBackupKeyRefusalPages(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		form   url.Values
		set    func(*fakeBackupKey)
		want   string
		absent []string
		golden string
	}{
		{"private key", url.Values{"mode": {"public"}, "public_key": {testBackupPrivate}},
			func(f *fakeBackupKey) { f.pubErr = ErrBackupPrivateKey },
			"This is a private key. Paste the public key (it starts with age1). Keep the private key off this server.",
			[]string{testBackupPrivate}, "key-private-error"},
		{"invalid key", url.Values{"mode": {"public"}, "public_key": {"nonsense"}},
			func(f *fakeBackupKey) { f.pubErr = ErrBackupKeyInvalid }, "Paste an age public key. It starts with age1.", nil, ""},
		{"short passphrase", url.Values{"mode": {"passphrase"}, "passphrase": {"short pass"}},
			func(f *fakeBackupKey) { f.passErr = ErrBackupPassphraseShort }, "Use at least 12 characters.", []string{"short pass"}, ""},
		{"generate without a key", url.Values{"mode": {"generate"}}, nil, "Generate a key first, or paste a public key.", nil, ""},
		{"no mode", url.Values{}, nil, "Choose how to encrypt backups.", nil, ""},
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
			if rec.Code != 422 || !strings.Contains(body, c.want) {
				t.Fatalf("save = %d, body lacks %q", rec.Code, c.want)
			}
			for _, s := range c.absent {
				if strings.Contains(body, s) {
					t.Errorf("the page echoes %q", s)
				}
			}
			assertPrivateFieldUnnamed(t, body)
			if c.golden != "" {
				compareGolden(t, "pages/settings-backups-"+c.golden+".html", normalizePage(body))
			}
		})
	}
}
