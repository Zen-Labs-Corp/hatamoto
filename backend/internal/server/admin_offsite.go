package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/offsite"
)

// offsiteTestTimeout bounds one connection test.
const offsiteTestTimeout = 20 * time.Second

// offsiteSettings is the part of offsite.Store the admin adapter uses.
type offsiteSettings interface {
	Get(ctx context.Context) (offsite.Settings, bool, error)
	Save(ctx context.Context, set offsite.Settings) error
}

// offsiteAdmin implements admin.OffsiteService over the saved settings and
// the upload service.
type offsiteAdmin struct {
	store offsiteSettings
	svc   *offsiteService
	key   []byte
	now   func() time.Time
}

func (o *offsiteAdmin) clock() time.Time {
	if o.now != nil {
		return o.now().UTC()
	}
	return time.Now().UTC()
}

func (o *offsiteAdmin) load(ctx context.Context) (offsite.Settings, error) {
	set, found, err := o.store.Get(ctx)
	if err != nil {
		return offsite.Settings{}, err
	}
	if !found {
		return offsite.Defaults(), nil
	}
	return set, nil
}

func (o *offsiteAdmin) View(ctx context.Context) (admin.OffsiteView, error) {
	set, err := o.load(ctx)
	if err != nil {
		return admin.OffsiteView{}, err
	}
	_, readable := set.Secret(o.key)
	v := admin.OffsiteView{
		Enabled: set.Enabled, Service: set.Service, Endpoint: set.Endpoint, Region: set.Region, Bucket: set.Bucket,
		Prefix: set.Prefix, AccessKey: set.AccessKey, Schedule: set.Schedule.Kind, Hour: set.Schedule.Hour,
		Weekday: set.Schedule.Weekday, Keep: set.Keep,
		SecretSaved: readable, SecretLost: set.HasSecret() && !readable,
		LastAt: set.Last.At, LastOK: set.Last.OK, LastSize: set.Last.Size, LastError: set.Last.Error,
	}
	if set.Enabled && set.Schedule.Kind != offsite.ScheduleOff {
		v.NextRun = set.NextDueAt
		if v.NextRun.IsZero() {
			v.NextRun = set.Schedule.Next(o.clock())
		}
	}
	return v, nil
}

// settings builds the settings an input describes on top of the saved ones.
func (o *offsiteAdmin) settings(saved offsite.Settings, in admin.OffsiteInput) (offsite.Settings, error) {
	set := offsite.Settings{
		Enabled: in.Enabled, Service: in.Service, Endpoint: in.Endpoint, Region: in.Region, Bucket: in.Bucket,
		Prefix: in.Prefix, AccessKey: in.AccessKey, SecretSealed: saved.SecretSealed,
		Schedule: offsite.Schedule{Kind: in.Schedule, Hour: in.Hour, Weekday: in.Weekday}, Keep: in.Keep,
		Last: saved.Last,
	}
	if in.SecretKey != "" {
		sealed, err := offsite.SealSecret(o.key, in.SecretKey)
		if err != nil {
			return set, err
		}
		set.SecretSealed = sealed
	}
	return set, nil
}

func (o *offsiteAdmin) Save(ctx context.Context, in admin.OffsiteInput) ([]ui.FieldError, error) {
	saved, err := o.load(ctx)
	if err != nil {
		return nil, err
	}
	set, err := o.settings(saved, in)
	if err != nil {
		return nil, err
	}
	if _, readable := set.Secret(o.key); set.Enabled && !readable {
		return []ui.FieldError{{ID: "offsite-secret-key", Label: "Secret key", Message: "Enter the secret key."}}, nil
	}
	if err := set.Validate(); err != nil {
		return []ui.FieldError{offsiteFieldError(err)}, nil
	}
	switch {
	case !set.Enabled || set.Schedule.Kind == offsite.ScheduleOff:
	case saved.Enabled && saved.Schedule == set.Schedule && !saved.NextDueAt.IsZero():
		set.NextDueAt = saved.NextDueAt // the slot is unchanged
	default:
		set.NextDueAt = set.Schedule.Next(o.clock())
	}
	return nil, o.store.Save(ctx, set)
}

func (o *offsiteAdmin) Test(ctx context.Context, in admin.OffsiteInput) error {
	saved, err := o.load(ctx)
	if err != nil {
		return err
	}
	set, err := o.settings(saved, in)
	if err != nil {
		return err
	}
	secret, ok := set.Secret(o.key)
	if !ok {
		return errors.New("enter the secret key")
	}
	t := offsite.Target{Endpoint: set.EffectiveEndpoint(), Region: set.Region, Bucket: set.Bucket,
		Prefix: set.Prefix, AccessKey: set.AccessKey, SecretKey: secret}
	if err := t.Validate(); err != nil {
		return err
	}
	c, err := t.Client(nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, offsiteTestTimeout)
	defer cancel()
	if err := c.Test(ctx); err != nil {
		return errors.New(o.svc.scrubText(secret, err.Error()))
	}
	return nil
}

func (o *offsiteAdmin) RunNow(ctx context.Context, actor string) (string, error) {
	id, err := o.svc.RunNow(ctx, actor)
	if errors.Is(err, backup.ErrBusy) {
		return "", admin.ErrBackupRunning
	}
	return id, err
}

// offsiteFieldError ties a validation message to the field it is about.
func offsiteFieldError(err error) ui.FieldError {
	msg := err.Error()
	low := strings.ToLower(msg)
	for _, f := range []struct{ word, id, label string }{
		{"region", "offsite-region", "Region"}, {"bucket", "offsite-bucket", "Bucket"},
		{"prefix", "offsite-prefix", "Folder prefix"}, {"access key", "offsite-access-key", "Access key"},
		{"secret key", "offsite-secret-key", "Secret key"}, {"keep", "offsite-keep", "Copies to keep"},
		{"hour", "offsite-hour", "Time of day"}, {"weekday", "offsite-weekday", "Day of the week"},
		{"schedule", "offsite-schedule", "Schedule"}, {"service", "offsite-service", "Storage service"},
	} {
		if strings.Contains(low, f.word) {
			return ui.FieldError{ID: f.id, Label: f.label, Message: capitalize(fullStopText(msg))}
		}
	}
	return ui.FieldError{ID: "offsite-endpoint", Label: "Endpoint", Message: capitalize(fullStopText(msg))}
}

func fullStopText(s string) string {
	if s != "" && !strings.ContainsAny(s[len(s)-1:], ".!?") {
		return s + "."
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
