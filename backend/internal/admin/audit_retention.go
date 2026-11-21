package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/auditlog"
)

const auditRetentionRel = auditRel + "/retention"

// AuditRetentionService is how long the daily cleanup keeps audit entries:
// admin entries and agent (MCP) calls each have their own period. The server
// implements it on the site config.
type AuditRetentionService interface {
	Days(ctx context.Context) (admin, agent int, err error)
	SetDays(ctx context.Context, admin, agent int) error
}

// auditRetentionData is the retention page: the saved (or, after a failed
// save, submitted) periods.
type auditRetentionData struct {
	Summary    []ui.FieldError
	Keep       []ui.Option
	KeepAgent  []ui.Option
	KeepError  string
	AgentError string
	Cancel     string
}

func (a *Admin) handleAuditRetention(w http.ResponseWriter, r *http.Request) {
	if a.deps.AuditRetention == nil {
		a.unavailable(w, r, "Audit log retention")
		return
	}
	admin, agent, err := a.deps.AuditRetention.Days(r.Context())
	if err != nil {
		a.logger().Error("audit retention settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The audit log retention could not be loaded.")
		return
	}
	a.renderAuditRetention(w, r, http.StatusOK, admin, agent, "", "")
}

func (a *Admin) handleAuditRetentionSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.AuditRetention
	if svc == nil {
		a.unavailable(w, r, "Audit log retention")
		return
	}
	ctx := r.Context()
	oldAdmin, oldAgent, err := svc.Days(ctx)
	if err != nil {
		a.logger().Error("audit retention settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The audit log retention could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	admin, errAdmin := parseAuditRetention(r.PostFormValue("keep"))
	agent, errAgent := parseAuditRetention(r.PostFormValue("keep_agent"))
	if errAdmin != "" || errAgent != "" {
		a.renderAuditRetention(w, r, http.StatusUnprocessableEntity, admin, agent, errAdmin, errAgent)
		return
	}
	if admin != oldAdmin || agent != oldAgent {
		if err := svc.SetDays(ctx, admin, agent); err != nil {
			a.logger().Error("audit retention save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The audit log retention could not be saved.")
			return
		}
		a.audit(r, "audit.retention", "audit-log", map[string]any{"old": oldAdmin, "new": admin, "old_agent": oldAgent, "new_agent": agent})
	}
	a.SetFlash(w, r, "success", "Audit log retention saved.")
	http.Redirect(w, r, a.adminPath+auditRetentionRel, http.StatusSeeOther)
}

// parseAuditRetention reads one submitted period; anything but an allowed
// value is an error message (and 0 days, which no select offers as selected).
func parseAuditRetention(raw string) (int, string) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !auditlog.ValidRetention(n) {
		return 0, "Choose one of the listed periods."
	}
	return n, ""
}

func auditRetentionOptions(selected int) []ui.Option {
	opts := make([]ui.Option, 0, len(auditlog.RetentionDays))
	for _, d := range auditlog.RetentionDays {
		opts = append(opts, ui.Option{Value: strconv.Itoa(d), Label: auditlog.RetentionLabel(d), Selected: d == selected})
	}
	return opts
}

func (a *Admin) renderAuditRetention(w http.ResponseWriter, r *http.Request, status, admin, agent int, errAdmin, errAgent string) {
	d := auditRetentionData{
		Keep: auditRetentionOptions(admin), KeepAgent: auditRetentionOptions(agent),
		KeepError: errAdmin, AgentError: errAgent, Cancel: a.adminPath + auditRel,
	}
	if errAdmin != "" {
		d.Summary = append(d.Summary, ui.FieldError{ID: "keep", Label: "Keep entries for", Message: errAdmin})
	}
	if errAgent != "" {
		d.Summary = append(d.Summary, ui.FieldError{ID: "keep_agent", Label: "Keep agent calls for", Message: errAgent})
	}
	v := a.NewView(w, r, "Audit log retention")
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "audit/retention", v)
}
