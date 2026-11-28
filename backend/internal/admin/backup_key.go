package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// Backup encryption modes as the page shows them.
const (
	BackupKeyOff        = "off"
	BackupKeyPublic     = "key"
	BackupKeyPassphrase = "passphrase"
)

var (
	// ErrBackupPrivateKey is returned by SavePublicKey for a private key.
	ErrBackupPrivateKey = errors.New("this is a private key")
	// ErrBackupKeyInvalid is returned by SavePublicKey for anything that is not an age public key.
	ErrBackupKeyInvalid = errors.New("not an age public key")
	// ErrBackupPassphraseShort is returned by SavePassphrase for a passphrase under 12 characters.
	ErrBackupPassphraseShort = errors.New("the passphrase is too short")
)

// BackupKeyView is the saved backup encryption as the page shows it. It never
// carries the passphrase or any private key.
type BackupKeyView struct {
	Mode       string // BackupKeyOff, BackupKeyPublic or BackupKeyPassphrase
	PublicKey  string // the age public key, empty unless Mode is BackupKeyPublic
	Unreadable bool   // encryption is on but its key cannot be read
}

// BackupKeyService is the backup encryption setting behind Settings > Backups.
type BackupKeyService interface {
	View(ctx context.Context) (BackupKeyView, error)
	// SavePublicKey turns encryption on with an age public key.
	// ErrBackupPrivateKey and ErrBackupKeyInvalid refuse the value.
	SavePublicKey(ctx context.Context, s string) error
	// SavePassphrase turns encryption on with a passphrase;
	// ErrBackupPassphraseShort refuses a short one.
	SavePassphrase(ctx context.Context, p string) error
	// Clear turns encryption off.
	Clear(ctx context.Context) error
}

// SetBackupKey hands the admin the backup encryption service. Call it before
// serving starts, and only with a non-nil service.
func (a *Admin) SetBackupKey(svc BackupKeyService) { a.deps.BackupKey = svc }

// mountBackupKey mounts the key routes behind the password: a hijacked session
// must not be able to redirect or lock future backups.
func (a *Admin) mountBackupKey(owner *httpx.Group) {
	sudo := owner.Group("", a.RequireSudo)
	sudo.HandleFunc("POST /settings/backups/encryption", a.handleBackupKeySave)
	sudo.HandleFunc("POST /settings/backups/encryption/off", a.handleBackupKeyOff)
}

// backupKeyData is the Backup encryption card. There is deliberately no
// passphrase and no private key value in it.
type backupKeyData struct {
	Mode       string
	PublicKey  string // shown in the public key field
	Unreadable bool
	LoadFailed bool // the saved state could not be read
	Status     ui.Badge
	StatusText string
	Errs       map[string]string // by field name: public_key, passphrase, mode
	Summary    []ui.FieldError
	Selected   string // the mode radio that is checked: generate, public or passphrase
	Modes      []ui.RadioCardItem
	// PlainOffsite counts off-site copies uploaded before encryption was on;
	// it is only shown while encryption is on. PlainNote is the sentence.
	PlainOffsite int
	PlainNote    string
}

// backupKeyCard builds the card from the saved state. posted, when set, is a
// failed submit to show again. It returns nil when the service is not wired.
// A state that cannot be read renders the card in an error state, not a 500.
func (a *Admin) backupKeyCard(ctx context.Context, st BackupsState, posted *backupKeyData) *backupKeyData {
	svc := a.deps.BackupKey
	if svc == nil {
		return nil
	}
	d := &backupKeyData{Mode: BackupKeyOff}
	v, err := svc.View(ctx)
	if err != nil {
		a.logger().Error("backup encryption view", "err", err)
		d.LoadFailed = true
		d.Status, d.StatusText = ui.Badge{Label: "Unavailable", Kind: "danger"}, "The backup encryption setting could not be read."
	} else {
		if v.Mode == "" {
			v.Mode = BackupKeyOff
		}
		d.Mode, d.PublicKey, d.Unreadable = v.Mode, v.PublicKey, v.Unreadable
		d.Status, d.StatusText = backupKeyStatus(v)
	}
	switch d.Mode {
	case BackupKeyPublic:
		d.Selected = "public"
	case BackupKeyPassphrase:
		d.Selected = "passphrase"
	default:
		d.Selected = "generate"
	}
	if d.Mode != BackupKeyOff {
		for _, b := range st.Rows {
			if b.Offsite == OffsiteUploaded && !b.OffsiteEncrypted {
				d.PlainOffsite++
			}
		}
	}
	if posted != nil {
		d.Selected, d.PublicKey, d.Errs, d.Summary = posted.Selected, posted.PublicKey, posted.Errs, posted.Summary
	}
	d.Modes = backupKeyModes(d.Selected)
	switch {
	case d.PlainOffsite == 1:
		d.PlainNote = "1 off-site copy was uploaded before encryption was on. It stays as it is."
	case d.PlainOffsite > 1:
		d.PlainNote = fmt.Sprintf("%d off-site copies were uploaded before encryption was on. They stay as they are.", d.PlainOffsite)
	}
	return d
}

func backupKeyModes(selected string) []ui.RadioCardItem {
	return []ui.RadioCardItem{
		{Value: "generate", Title: "Generate a key pair for me", Checked: selected == "generate"},
		{Value: "public", Title: "I have a public key", Checked: selected == "public"},
		{Value: "passphrase", Title: "Use a passphrase instead", Checked: selected == "passphrase"},
	}
}

func backupKeyStatus(v BackupKeyView) (ui.Badge, string) {
	switch {
	case v.Mode == BackupKeyOff:
		return ui.Badge{Label: "Off", Kind: "muted"}, "Downloads and off-site copies are not encrypted."
	case v.Unreadable:
		return ui.Badge{Label: "Key unreadable", Kind: "danger"}, "The saved key cannot be read. Nothing is downloaded or copied off-site until you set it again."
	case v.Mode == BackupKeyPassphrase:
		return ui.Badge{Label: "On: passphrase", Kind: "success"}, "Downloads and off-site copies are encrypted with the passphrase."
	}
	return ui.Badge{Label: "On: public key set", Kind: "success"}, "Downloads and off-site copies are encrypted to the public key below."
}

func (a *Admin) backupKeyService(w http.ResponseWriter, r *http.Request) (BackupKeyService, bool) {
	if a.deps.BackupKey == nil || a.deps.Backups == nil {
		a.unavailable(w, r, "Backup encryption")
		return nil, false
	}
	return a.deps.BackupKey, true
}

// rerenderBackupKey shows the Backups page again with a field error.
func (a *Admin) rerenderBackupKey(w http.ResponseWriter, r *http.Request, posted *backupKeyData) {
	st, err := a.deps.Backups.List(r.Context())
	if err != nil {
		a.logger().Error("backups list", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backups could not be listed.")
		return
	}
	of, ok := a.offsiteCardOrError(w, r)
	if !ok {
		return
	}
	a.renderBackupsKey(w, r, http.StatusUnprocessableEntity, st, of, a.backupKeyCard(r.Context(), st, posted))
}

func (a *Admin) handleBackupKeySave(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.backupKeyService(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	mode := r.PostFormValue("mode")
	posted := &backupKeyData{Selected: mode, PublicKey: strings.TrimSpace(r.PostFormValue("public_key"))}
	fail := func(field, id, label, msg string) {
		posted.Errs = map[string]string{field: msg}
		posted.Summary = []ui.FieldError{{ID: id, Label: label, Message: msg}}
		a.rerenderBackupKey(w, r, posted)
	}
	var err error
	auditMode := BackupKeyPublic
	switch mode {
	case "generate", "public":
		if mode == "generate" && posted.PublicKey == "" {
			fail("public_key", "backupkey-public", "Public key", "Generate a key first, or paste a public key.")
			return
		}
		err = svc.SavePublicKey(r.Context(), posted.PublicKey)
	case "passphrase":
		auditMode = BackupKeyPassphrase
		err = svc.SavePassphrase(r.Context(), r.PostFormValue("passphrase"))
	default:
		posted.Selected = ""
		fail("mode", "backupkey-mode", "Backup encryption", "Choose how to encrypt backups.")
		return
	}
	switch {
	case err == nil:
	case errors.Is(err, ErrBackupPrivateKey):
		posted.PublicKey = "" // never show a private key again
		fail("public_key", "backupkey-public", "Public key",
			"This is a private key. Paste the public key (it starts with age1). Keep the private key off this server.")
		return
	case errors.Is(err, ErrBackupKeyInvalid):
		fail("public_key", "backupkey-public", "Public key", "Paste an age public key. It starts with age1.")
		return
	case errors.Is(err, ErrBackupPassphraseShort):
		fail("passphrase", "backupkey-passphrase", "Passphrase", "Use at least 12 characters.")
		return
	default:
		a.logger().Error("backup encryption save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backup encryption could not be saved.")
		return
	}
	a.audit(r, "backups.encryption.update", "backups", map[string]any{"mode": auditMode})
	a.SetFlash(w, r, "success", "Backup encryption is on. Downloads and off-site copies are encrypted from now on.")
	http.Redirect(w, r, a.adminPath+backupsRel+"#backupkey", http.StatusSeeOther)
}

func (a *Admin) handleBackupKeyOff(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.backupKeyService(w, r)
	if !ok {
		return
	}
	if err := svc.Clear(r.Context()); err != nil {
		a.logger().Error("backup encryption clear", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backup encryption could not be turned off.")
		return
	}
	a.audit(r, "backups.encryption.off", "backups", map[string]any{})
	a.SetFlash(w, r, "success", "Backup encryption is off. New downloads and off-site copies are not encrypted.")
	http.Redirect(w, r, a.adminPath+backupsRel+"#backupkey", http.StatusSeeOther)
}
