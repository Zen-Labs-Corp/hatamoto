package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/mailsettings"
)

// emailAdmin is the admin's view of the SMTP settings: the store, the key that
// seals the password, and hooks to the live mail service.
type emailAdmin struct {
	store   smtpSettingsStore
	key     []byte
	apply   func(ctx context.Context) emailResolution
	current func() emailResolution
}

// applyTimeout bounds the re-resolve after a save or reset.
const applyTimeout = 10 * time.Second

// applyDetached re-resolves the live configuration after the row changed. The
// request context is not used for cancellation: a client that disconnects
// after the commit must not leave mail on the wrong configuration.
func (a emailAdmin) applyDetached(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyTimeout)
	defer cancel()
	a.apply(ctx)
}

func (a emailAdmin) Get(ctx context.Context) (admin.EmailSettingsView, error) {
	set, found, err := a.store.Get(ctx)
	if err != nil {
		return admin.EmailSettingsView{}, err
	}
	res := a.current()
	v := admin.EmailSettingsView{
		Provider: mailsettings.ProviderCustom,
		Port:     587,
		Security: email.SecurityStarttls,
		Source:   admin.EmailSource(res.Source),
		Disabled: os.Getenv("EMAIL_DISABLED") == "true",
	}
	v.Configured = res.Source != emailSourceNone
	if found {
		v.Saved = true
		v.Provider, v.SESRegion, v.Host, v.Port = set.Provider, set.SESRegion, set.Host, set.Port
		v.Security, v.Username = set.Security, set.Username
		v.FromAddress, v.FromName = set.FromAddress, set.FromName
		if set.HasPassword() {
			_, ok := set.Password(a.key)
			v.PasswordSaved, v.PasswordLost = ok, !ok
		}
	}
	return v, nil
}

// settings builds the row for in. With keepPassword the saved sealed password
// is carried over (and returned opened for the connection check).
func (a emailAdmin) settings(ctx context.Context, in admin.EmailInput, keepPassword bool) (mailsettings.Settings, string, error) {
	set := mailsettings.Settings{
		Provider: in.Provider, SESRegion: in.SESRegion, Host: in.Host, Port: in.Port,
		Security: in.Security, Username: in.Username,
		FromAddress: in.FromAddress, FromName: in.FromName,
	}
	password := in.Password
	if keepPassword {
		stored, found, err := a.store.Get(ctx)
		if err != nil {
			return set, "", err
		}
		// The saved password only ever goes to the host and user it was saved
		// for, whatever the caller validated.
		if !found || stored.Host != in.Host || stored.Username != in.Username {
			return set, "", errors.New("the saved password belongs to a different host or username; enter it again")
		}
		var ok bool
		if password, ok = stored.Password(a.key); !ok {
			return set, "", errors.New("the saved password cannot be read; enter it again")
		}
		set.PasswordSealed = stored.PasswordSealed
		return set, password, nil
	}
	if password != "" {
		sealed, err := mailsettings.SealPassword(a.key, password)
		if err != nil {
			return set, "", fmt.Errorf("seal smtp password: %w", err)
		}
		set.PasswordSealed = sealed
	}
	return set, password, nil
}

func (a emailAdmin) Check(ctx context.Context, in admin.EmailInput, keepPassword bool) error {
	set, password, err := a.settings(ctx, in, keepPassword)
	if err != nil {
		return err
	}
	return email.Check(ctx, set.EmailConfig(password))
}

func (a emailAdmin) Save(ctx context.Context, in admin.EmailInput, keepPassword bool) error {
	set, _, err := a.settings(ctx, in, keepPassword)
	if err != nil {
		return err
	}
	if err := a.store.Save(ctx, set); err != nil {
		return err
	}
	a.applyDetached(ctx)
	return nil
}

func (a emailAdmin) Reset(ctx context.Context) error {
	if err := a.store.Clear(ctx); err != nil {
		return err
	}
	a.applyDetached(ctx)
	return nil
}
