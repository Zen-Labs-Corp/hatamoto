package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// Off-site services as the form offers them. They mirror the offsite package;
// the admin does not import it, so the server adapter maps.
const (
	OffsiteAWS    = "aws"
	OffsiteR2     = "r2"
	OffsiteB2     = "b2"
	OffsiteMinIO  = "minio"
	OffsiteCustom = "custom"

	OffsiteOff    = "off"
	OffsiteDaily  = "daily"
	OffsiteWeekly = "weekly"
)

// Backup copies the off-site upload marks on a backup row.
const (
	OffsiteUploaded = "uploaded"
	OffsiteFailed   = "failed"
)

// OffsiteView is the saved off-site configuration and its latest result. It
// never carries the secret key.
type OffsiteView struct {
	Enabled   bool
	Service   string
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	Schedule  string // OffsiteOff, OffsiteDaily or OffsiteWeekly
	Hour      int    // UTC
	Weekday   int    // 0 is Sunday
	Keep      int

	// SecretSaved: a secret key is stored and can be read. SecretLost: one is
	// stored but cannot be read (the server SECRET changed).
	SecretSaved bool
	SecretLost  bool

	NextRun   time.Time // zero when nothing is scheduled
	LastAt    time.Time // zero: nothing uploaded yet
	LastOK    bool
	LastSize  int64
	LastError string
}

// OffsiteInput is one submitted form. SecretKey is empty when the owner left
// it blank, which keeps the saved key.
type OffsiteInput struct {
	Enabled   bool
	Service   string
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	SecretKey string
	Schedule  string
	Hour      int
	Weekday   int
	Keep      int
}

// OffsiteService is the off-site backup target behind Settings > Backups & recovery > Backups.
type OffsiteService interface {
	View(ctx context.Context) (OffsiteView, error)
	// Save stores in. Field errors come back as the list and nothing is saved.
	Save(ctx context.Context, in OffsiteInput) ([]ui.FieldError, error)
	// Test opens the target in describes (with the saved key when SecretKey
	// is blank) without storing anything. The error is safe to show.
	Test(ctx context.Context, in OffsiteInput) error
	// RunNow starts a backup that is uploaded when it is done and returns its
	// id; ErrBackupRunning when another backup holds the lock.
	RunNow(ctx context.Context, actor string) (string, error)
}

// SetOffsite hands the admin the off-site service once the backup engine is
// known. Call it before serving starts, and only with a non-nil service.
func (a *Admin) SetOffsite(svc OffsiteService) { a.deps.Offsite = svc }

func (a *Admin) mountOffsite(owner *httpx.Group) {
	owner.HandleFunc("POST /settings/backups/offsite", a.handleOffsiteSave)
	owner.HandleFunc("POST /settings/backups/offsite/test", a.handleOffsiteTest)
	owner.HandleFunc("POST /settings/backups/offsite/run", a.handleOffsiteRun)
}

// offsiteFields is the form as shown. There is deliberately no secret key value.
type offsiteFields struct {
	Errs map[string]string

	Enabled         bool
	ServiceOptions  []ui.Option
	ScheduleOptions []ui.Option
	HourOptions     []ui.Option
	WeekdayOptions  []ui.Option
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKey       string
	Keep            string

	SecretSaved bool
	SecretLost  bool
}

// offsiteData is the Off-site card.
type offsiteData struct {
	Form    offsiteFields
	Summary []ui.FieldError
	// Notice is the result of Test connection, shown above the form.
	Notice     string
	NoticeKind string // success or error
	Status     ui.Badge
	StatusText string
	NextRun    string
	CanRun     bool
}

var offsiteServices = []struct{ v, label string }{
	{OffsiteAWS, "Amazon S3"}, {OffsiteR2, "Cloudflare R2"}, {OffsiteB2, "Backblaze B2"},
	{OffsiteMinIO, "MinIO"}, {OffsiteCustom, "Other S3-compatible storage"},
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// offsiteInputFrom reads the posted form; it reports the numbers that are not numbers.
func offsiteInputFrom(form url.Values) (OffsiteInput, []ui.FieldError) {
	var errs []ui.FieldError
	typed := func(k string) string { return strings.TrimSpace(form.Get(k)) }
	num := func(k, id, label string, lo, hi int) int {
		n, err := strconv.Atoi(typed(k))
		if err != nil || n < lo || n > hi {
			errs = append(errs, ui.FieldError{ID: id, Label: label, Message: "Enter a whole number from " + strconv.Itoa(lo) + " to " + strconv.Itoa(hi) + "."})
		}
		return n
	}
	in := OffsiteInput{
		Enabled: form.Get("enabled") == "true", Service: typed("service"), Endpoint: typed("endpoint"),
		Region: typed("region"), Bucket: typed("bucket"), Prefix: typed("prefix"), AccessKey: typed("access_key"),
		SecretKey: form.Get("secret_key"), Schedule: typed("schedule"),
	}
	in.Hour = num("hour", "offsite-hour", "Hour", 0, 23)
	in.Weekday = num("weekday", "offsite-weekday", "Day of the week", 0, 6)
	in.Keep = num("keep", "offsite-keep", "Copies to keep", 1, 365)
	return in, errs
}

func newOffsiteFields(v OffsiteView, form url.Values, errs []ui.FieldError) offsiteFields {
	service, schedule := v.Service, v.Schedule
	hour, weekday := strconv.Itoa(v.Hour), strconv.Itoa(v.Weekday)
	f := offsiteFields{
		Errs: map[string]string{}, Enabled: v.Enabled, Endpoint: v.Endpoint, Region: v.Region, Bucket: v.Bucket,
		Prefix: v.Prefix, AccessKey: v.AccessKey, Keep: strconv.Itoa(v.Keep),
		SecretSaved: v.SecretSaved, SecretLost: v.SecretLost,
	}
	if form != nil {
		f.Enabled = form.Get("enabled") == "true"
		service, schedule = form.Get("service"), form.Get("schedule")
		hour, weekday, f.Keep = form.Get("hour"), form.Get("weekday"), strings.TrimSpace(form.Get("keep"))
		f.Endpoint, f.Region, f.Bucket = strings.TrimSpace(form.Get("endpoint")), strings.TrimSpace(form.Get("region")), strings.TrimSpace(form.Get("bucket"))
		f.Prefix, f.AccessKey = strings.TrimSpace(form.Get("prefix")), strings.TrimSpace(form.Get("access_key"))
	}
	if service == "" {
		service = OffsiteAWS
	}
	if schedule == "" {
		schedule = OffsiteOff
	}
	for _, s := range offsiteServices {
		f.ServiceOptions = append(f.ServiceOptions, ui.Option{Value: s.v, Label: s.label, Selected: service == s.v})
	}
	for _, s := range []struct{ v, label string }{{OffsiteOff, "Off"}, {OffsiteDaily, "Every day"}, {OffsiteWeekly, "Every week"}} {
		f.ScheduleOptions = append(f.ScheduleOptions, ui.Option{Value: s.v, Label: s.label, Selected: schedule == s.v})
	}
	for h := 0; h < 24; h++ {
		f.HourOptions = append(f.HourOptions, ui.Option{Value: strconv.Itoa(h), Label: pad2(h) + ":00 UTC", Selected: hour == strconv.Itoa(h)})
	}
	for d, name := range weekdayNames {
		f.WeekdayOptions = append(f.WeekdayOptions, ui.Option{Value: strconv.Itoa(d), Label: name, Selected: weekday == strconv.Itoa(d)})
	}
	for _, e := range errs {
		f.Errs[e.ID] = e.Message
	}
	return f
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// offsiteStatus is the status line of the card: a badge and one sentence.
func offsiteStatus(v OffsiteView, loc *time.Location) (ui.Badge, string, string) {
	next := ""
	if v.Enabled && !v.NextRun.IsZero() {
		next = "Next run: " + fmtDateTime(v.NextRun.UTC()) + " UTC."
	}
	switch {
	case !v.Enabled:
		return ui.Badge{Label: "Off", Kind: "muted"}, "Backups are not copied off this server.", ""
	case v.LastAt.IsZero():
		return ui.Badge{Label: "Waiting", Kind: "info"}, "Nothing has been uploaded yet.", next
	case !v.LastOK:
		msg := "The last upload, on " + fmtDateTime(v.LastAt.In(loc)) + ", failed."
		if e := strings.TrimSpace(v.LastError); e != "" {
			msg += " " + fullStop(e)
		}
		return ui.Badge{Label: "Failed", Kind: "danger"}, msg, next
	}
	return ui.Badge{Label: "OK", Kind: "success"}, "Last upload " + fmtDateTime(v.LastAt.In(loc)) + ", " + backupSize(v.LastSize) + ", size and checksum read back.", next
}

// offsiteCard builds the card data. form is the posted form to show again (nil
// shows the saved values).
func (a *Admin) offsiteCard(loc *time.Location, v OffsiteView, form url.Values, errs []ui.FieldError, notice, kind string) *offsiteData {
	d := &offsiteData{Form: newOffsiteFields(v, form, errs), Summary: errs, Notice: notice, NoticeKind: kind,
		CanRun: v.Enabled && (v.SecretSaved)}
	d.Status, d.StatusText, d.NextRun = offsiteStatus(v, loc)
	return d
}

func (a *Admin) offsiteService(w http.ResponseWriter, r *http.Request) (OffsiteService, OffsiteView, bool) {
	svc := a.deps.Offsite
	if svc == nil || a.deps.Backups == nil {
		a.unavailable(w, r, "Off-site backups")
		return nil, OffsiteView{}, false
	}
	v, err := svc.View(r.Context())
	if err != nil {
		a.logger().Error("offsite settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The off-site settings could not be loaded.")
		return nil, OffsiteView{}, false
	}
	return svc, v, true
}

// rerenderOffsite shows the Backups page again with the posted form.
func (a *Admin) rerenderOffsite(w http.ResponseWriter, r *http.Request, status int, v OffsiteView, errs []ui.FieldError, notice, kind string) {
	st, err := a.deps.Backups.List(r.Context())
	if err != nil {
		a.logger().Error("backups list", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backups could not be listed.")
		return
	}
	of := a.offsiteCard(a.siteLocation(r.Context()), v, r.PostForm, errs, notice, kind)
	a.renderBackups(w, r, status, st, of)
}

func (a *Admin) handleOffsiteSave(w http.ResponseWriter, r *http.Request) {
	svc, stored, ok := a.offsiteService(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	in, errs := offsiteInputFrom(r.PostForm)
	if len(errs) == 0 {
		var err error
		if errs, err = svc.Save(r.Context(), in); err != nil {
			a.logger().Error("offsite settings save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The off-site settings could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		a.rerenderOffsite(w, r, http.StatusUnprocessableEntity, stored, errs, "", "")
		return
	}
	a.audit(r, "backups.offsite.update", "backups", map[string]any{
		"changed": offsiteChanged(stored, in), "secret_changed": in.SecretKey != "", "enabled": in.Enabled,
	})
	a.SetFlash(w, r, "success", "Off-site settings saved.")
	http.Redirect(w, r, a.adminPath+backupsRel+"#offsite", http.StatusSeeOther)
}

// offsiteChanged names the fields that differ from the stored ones; values
// stay out of the audit log.
func offsiteChanged(v OffsiteView, in OffsiteInput) []string {
	changed := []string{}
	for _, c := range []struct {
		name string
		diff bool
	}{
		{"enabled", v.Enabled != in.Enabled}, {"service", v.Service != in.Service}, {"endpoint", v.Endpoint != in.Endpoint},
		{"region", v.Region != in.Region}, {"bucket", v.Bucket != in.Bucket}, {"prefix", v.Prefix != in.Prefix},
		{"access_key", v.AccessKey != in.AccessKey}, {"schedule", v.Schedule != in.Schedule},
		{"hour", v.Hour != in.Hour}, {"weekday", v.Weekday != in.Weekday}, {"keep", v.Keep != in.Keep},
	} {
		if c.diff {
			changed = append(changed, c.name)
		}
	}
	return changed
}

func (a *Admin) handleOffsiteTest(w http.ResponseWriter, r *http.Request) {
	svc, stored, ok := a.offsiteService(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	in, errs := offsiteInputFrom(r.PostForm)
	if len(errs) > 0 {
		a.rerenderOffsite(w, r, http.StatusUnprocessableEntity, stored, errs, "", "")
		return
	}
	if err := svc.Test(r.Context(), in); err != nil {
		a.rerenderOffsite(w, r, http.StatusUnprocessableEntity, stored, nil, "The connection failed: "+fullStop(err.Error())+" Nothing was saved.", "error")
		return
	}
	a.rerenderOffsite(w, r, http.StatusOK, stored, nil, "The bucket answered and the keys work. Nothing was saved yet.", "success")
}

func (a *Admin) handleOffsiteRun(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := a.offsiteService(w, r)
	if !ok {
		return
	}
	actor := ""
	if u := userFrom(r.Context()); u != nil {
		actor = u.Email
	}
	id, err := svc.RunNow(r.Context(), actor)
	switch {
	case errors.Is(err, ErrBackupRunning):
		a.SetFlash(w, r, "warning", "A backup is already running.")
	case err != nil:
		a.logger().Error("offsite run", "err", err)
		a.SetFlash(w, r, "error", "The backup could not be started: "+fullStop(err.Error()))
	default:
		a.audit(r, "backups.offsite.run", id, map[string]any{"id": id})
		a.SetFlash(w, r, "info", "Backup started. It is copied off the server when it is done.")
	}
	http.Redirect(w, r, a.adminPath+backupsRel, http.StatusSeeOther)
}
