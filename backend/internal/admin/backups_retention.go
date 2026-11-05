package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
)

// Bounds of a retention count, the same for every kind.
const (
	retentionMin = 1
	retentionMax = 50
)

// RetentionView is how many backups of each kind stay on disk, as the engine
// resolves it (environment, then the saved file, then the default).
type RetentionView struct {
	Manual, PreUpdate, Scheduled int
	// Locked and EnvVar are keyed by BackupManual, BackupPreUpdate and
	// BackupScheduled. A locked kind is set by the environment variable named
	// in EnvVar and cannot be changed here.
	Locked map[string]bool
	EnvVar map[string]string
}

// RetentionService is the optional second half of the backup engine: the
// counts the owner can change. The admin finds it by type assertion on
// Deps.Backups; without it the page shows the Keeping line only.
type RetentionService interface {
	Retention() RetentionView
	// SaveRetention stores the counts (only the kinds in set) and prunes by
	// the new counts at once. It returns how many backups it deleted per kind.
	SaveRetention(ctx context.Context, set map[string]int) (deleted map[string]int, err error)
}

// retentionKinds lists the three kinds in page order with their field ids and
// labels.
var retentionKinds = []struct{ kind, id, label, noun string }{
	{BackupManual, "manual", "Manual", "manual"},
	{BackupPreUpdate, "pre-update", "Pre-update", "pre-update"},
	{BackupScheduled, "scheduled", "Scheduled", "scheduled"},
}

func (v RetentionView) count(kind string) int {
	switch kind {
	case BackupManual:
		return v.Manual
	case BackupPreUpdate:
		return v.PreUpdate
	}
	return v.Scheduled
}

type retentionField struct {
	ID, Name, Label, Value, Help, Error string
	Attrs                               map[string]string
}

type retentionData struct {
	Fields  []retentionField
	Summary []ui.FieldError
	// AllLocked is true when the environment fixes every count: nothing to save.
	AllLocked bool
}

// retentionCard builds the form. form and errs are the posted values of a
// rejected save; a locked kind always shows the effective value.
func retentionCard(v RetentionView, form url.Values, errs []ui.FieldError) *retentionData {
	d := &retentionData{Summary: errs, AllLocked: true}
	for _, k := range retentionKinds {
		f := retentionField{ID: k.id, Name: strings.ReplaceAll(k.id, "-", "_"), Label: k.label, Value: strconv.Itoa(v.count(k.kind)), Help: "1 to 50",
			Attrs: map[string]string{"min": "1", "max": "50", "step": "1"}}
		d.AllLocked = d.AllLocked && v.Locked[k.kind]
		switch {
		case v.Locked[k.kind]:
			f.Attrs = map[string]string{"readonly": "readonly"}
			f.Help = "Set by " + v.EnvVar[k.kind] + ". Change it in the server config."
		case form != nil:
			f.Value = strings.TrimSpace(form.Get(f.Name))
		}
		for _, e := range errs {
			if e.ID == k.id {
				f.Error = e.Message
			}
		}
		d.Fields = append(d.Fields, f)
	}
	return d
}

// retentionService is the service behind the form, or nil when the engine has
// no retention to change.
func (a *Admin) retentionService() RetentionService {
	rs, _ := a.deps.Backups.(RetentionService)
	return rs
}

// retentionDrops counts the finished backups each kind would lose when it
// keeps keep[kind]: the oldest beyond the newest ones, like the engine's prune.
func retentionDrops(rows []BackupInfo, keep map[string]int) map[string]int {
	seen, drops := map[string]int{}, map[string]int{}
	for _, b := range rows { // newest first
		if b.State != BackupOK {
			continue
		}
		seen[b.Kind]++
		if seen[b.Kind] > keep[b.Kind] {
			drops[b.Kind]++
		}
	}
	return drops
}

// expectPrefix names the hidden fields that carry the counts the confirm page showed.
const expectPrefix = "expect_"

// sameDrops reports whether the counts posted back from the confirm page equal drops.
func sameDrops(drops map[string]int, form url.Values) bool {
	for _, k := range retentionKinds {
		shown, _ := strconv.Atoi(form.Get(expectPrefix + strings.ReplaceAll(k.id, "-", "_")))
		if shown != drops[k.kind] {
			return false
		}
	}
	return true
}

// deletesSentence says what a save removes: "This deletes 4 manual backups
// (the oldest)." with one clause per kind.
func deletesSentence(drops map[string]int) string {
	var parts []string
	for _, k := range retentionKinds {
		switch n := drops[k.kind]; n {
		case 0:
		case 1:
			parts = append(parts, "1 "+k.noun+" backup")
		default:
			parts = append(parts, fmt.Sprintf("%d %s backups", n, k.noun))
		}
	}
	return "This deletes " + strings.Join(parts, ", ") + " (the oldest). They cannot be recovered."
}

// retentionPage is the data of the Backups to keep page: the form and where
// Cancel goes.
type retentionPage struct {
	*retentionData
	Cancel string
}

func (a *Admin) renderRetentionPage(w http.ResponseWriter, r *http.Request, status int, d *retentionData) {
	v := a.NewView(w, r, "Backups to keep")
	v.Section, v.Tab, v.Width = "backups-recovery", "backups", "narrow"
	v.Crumbs = a.areaCrumbs("backups", Crumb{Label: "Backups to keep"})
	v.Data = retentionPage{retentionData: d, Cancel: a.adminPath + backupsRel}
	a.Render(w, r, status, "settings/backups-retention", v)
}

func (a *Admin) handleRetentionPage(w http.ResponseWriter, r *http.Request) {
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return
	}
	rs := a.retentionService()
	if rs == nil {
		a.unavailable(w, r, "Backup retention")
		return
	}
	a.renderRetentionPage(w, r, http.StatusOK, retentionCard(rs.Retention(), nil, nil))
}

func (a *Admin) handleRetentionSave(w http.ResponseWriter, r *http.Request) {
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return
	}
	rs := a.retentionService()
	if rs == nil {
		a.unavailable(w, r, "Backup retention")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	view := rs.Retention()
	set, keep := map[string]int{}, map[string]int{}
	var errs []ui.FieldError
	for _, k := range retentionKinds {
		if view.Locked[k.kind] { // the environment wins; a posted value is ignored
			keep[k.kind] = view.count(k.kind)
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get(strings.ReplaceAll(k.id, "-", "_"))))
		if err != nil || n < retentionMin || n > retentionMax {
			errs = append(errs, ui.FieldError{ID: k.id, Label: k.label, Message: "Choose a number from 1 to 50."})
			continue
		}
		set[k.kind], keep[k.kind] = n, n
	}
	st, err := a.deps.Backups.List(r.Context())
	if err != nil {
		a.logger().Error("backups list", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backups could not be listed.")
		return
	}
	if len(errs) > 0 {
		a.renderRetentionPage(w, r, http.StatusUnprocessableEntity, retentionCard(view, r.PostForm, errs))
		return
	}
	// A confirmed post only counts when what would be deleted now is what the
	// confirm page showed; otherwise the page is shown again with the new numbers.
	if drops := retentionDrops(st.Rows, keep); len(drops) > 0 && (r.PostForm.Get("confirm") != "1" || !sameDrops(drops, r.PostForm)) {
		d := confirmData{
			Title: "Keep fewer backups?", Label: "Delete and save", Danger: true,
			Message: deletesSentence(drops),
			Post:    a.adminPath + backupsRel + "/retention", Back: a.adminPath + backupsRel + "/retention",
			Hidden: []hiddenField{{Name: "confirm", Value: "1"}},
		}
		for _, k := range retentionKinds {
			if n := drops[k.kind]; n > 0 {
				d.Hidden = append(d.Hidden, hiddenField{Name: expectPrefix + strings.ReplaceAll(k.id, "-", "_"), Value: strconv.Itoa(n)})
			}
			if n, ok := set[k.kind]; ok {
				d.Hidden = append(d.Hidden, hiddenField{Name: strings.ReplaceAll(k.id, "-", "_"), Value: strconv.Itoa(n)})
			}
		}
		a.renderConfirm(w, r, d)
		return
	}
	deleted, err := rs.SaveRetention(r.Context(), set)
	pruneFailed := errors.Is(err, ErrRetentionPrune)
	deferred := errors.Is(err, ErrRetentionDeferred)
	if err != nil && !pruneFailed && !deferred {
		a.logger().Error("backup retention save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backup counts could not be saved.")
		return
	}
	total := 0
	for _, n := range deleted {
		total += n
	}
	detail := map[string]any{
		"manual": keep[BackupManual], "pre_update": keep[BackupPreUpdate], "scheduled": keep[BackupScheduled], "deleted": total,
	}
	if pruneFailed {
		a.logger().Error("backup retention prune", "err", err)
		detail["error"] = "prune_failed"
	}
	if deferred {
		detail["deferred"] = true
	}
	a.audit(r, "backups.retention", "backups", detail)
	switch {
	case deferred:
		a.SetFlash(w, r, "warning", "Backup retention saved. A backup is running, so older backups were not deleted yet. Save again once it finishes.")
	case pruneFailed:
		a.SetFlash(w, r, "error", "The backup counts were saved, but removing the older backups failed. Check the server log.")
	default:
		a.SetFlash(w, r, "success", "Backup retention saved.")
	}
	http.Redirect(w, r, a.adminPath+backupsRel, http.StatusSeeOther)
}
