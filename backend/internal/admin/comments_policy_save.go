package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/authz"
)

// cpolConfirmView is the data of the no-JS confirm page.
type cpolConfirmView struct {
	Type SchemaCommentConfig
	Vals url.Values
	Err  string
}

// handleCommentPolicySave saves one content type's order and community
// policy. Turning the policy on needs the tick; turning it off keeps the
// stored numbers.
func (a *Admin) handleCommentPolicySave(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionCommentSettingsManage) {
		return
	}
	svc := a.deps.Comments
	if svc == nil || !svc.Supports("schema-policy") {
		a.unavailable(w, r, "Comments")
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	cfgs, err := svc.SchemaCommentConfigs(ctx)
	if err != nil {
		a.serverError(w, r, "The content types could not be loaded", err)
		return
	}
	var cur *SchemaCommentConfig
	for i := range cfgs {
		if cfgs[i].ID == id {
			cur = &cfgs[i]
		}
	}
	if cur == nil {
		a.notFound(w, r, "Content type", a.csetPath(), "Back to comments")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty: nothing changes
	form := r.PostForm
	_, rankOK := a.cpolModerators()[cur.Slug]
	next, order, errs := cpolParse(form, *cur, rankOK)
	if len(errs) == 0 && !cur.Policy.Enabled && next.Enabled && form.Get("understand") != "1" {
		v := a.NewView(w, r, "Turn the community policy on?")
		v.Section, v.Tab = "comments", "comments"
		v.Width = "compact"
		v.Data = cpolConfirmView{Type: *cur, Vals: form, Err: "Tick the box to confirm you understand."}
		a.Render(w, r, http.StatusUnprocessableEntity, "settings/comments-policy", v)
		return
	}
	if len(errs) == 0 {
		err = svc.SetSchemaComments(ctx, id, order, next)
		var fe *CommentFieldError
		switch {
		case errors.As(err, &fe):
			errs = map[string]string{fe.Field: fe.Message}
		case errors.Is(err, ErrCommentsUnsupported):
			a.SetFlash(w, r, "error", "This content type cannot use the community policy yet.")
			Redirect(w, r, a.csetPath()+"#comments-type-"+cur.Slug)
			return
		case errors.Is(err, ErrCommentNotFound):
			a.notFound(w, r, "Content type", a.csetPath(), "Back to comments")
			return
		case err != nil:
			a.serverError(w, r, "The community policy could not be saved", err)
			return
		}
	}
	if len(errs) > 0 {
		s, err := svc.Settings(ctx)
		if err != nil {
			a.serverError(w, r, "The comment settings could not be loaded", err)
			return
		}
		a.renderCommentSettingsPosted(w, r, http.StatusUnprocessableEntity, s, csetValues(s), nil, &cpolPosted{ID: id, Vals: form, Errs: errs})
		return
	}
	a.audit(r, "comments.policy", id, map[string]any{
		"type": cur.Slug, "old": cur.Policy.Enabled, "new": next.Enabled, "changed": cpolChanged(*cur, next, order),
	})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(ctx)
	}
	a.SetFlash(w, r, "success", "Saved.")
	Redirect(w, r, a.csetPath()+"#comments-type-"+cur.Slug)
}

// cpolParse reads the posted switch, numbers and order over the stored
// values. An absent field keeps the stored value; a bad one is an error. "rank"
// is accepted while a plugin holds comments.moderate for the type, or when stored.
func cpolParse(form url.Values, cur SchemaCommentConfig, rankOK bool) (next CommentPolicy, order string, errs map[string]string) {
	next, order, errs = cur.Policy, cur.Order, map[string]string{}
	if form.Has("policy") {
		next.Enabled = form.Get("policy") == "true"
	}
	ints := map[string]*int{"max_depth": &next.MaxDepth, "edit_window_minutes": &next.EditWindowMinutes, "slow_mode_seconds": &next.SlowModeSeconds}
	for _, f := range cpolFields {
		raw := strings.TrimSpace(form.Get(f.Name))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < f.Min || n > f.Max {
			errs[f.Name] = "Enter a whole number from " + strconv.Itoa(f.Min) + " to " + strconv.Itoa(f.Max) + "."
			continue
		}
		*ints[f.Name] = n
	}
	if form.Has("order") {
		order = form.Get("order")
		if order != "" && order != "newest" && order != "oldest" && order != cur.Order && !(rankOK && order == "rank") {
			errs["order"] = "Choose the site default, newest first or oldest first."
		}
	}
	if len(errs) == 0 {
		errs = nil
	}
	return next, order, errs
}

// cpolChanged names the fields that differ. Names only: the audit detail
// never holds values.
func cpolChanged(cur SchemaCommentConfig, next CommentPolicy, order string) []string {
	out := []string{}
	if cur.Policy.Enabled != next.Enabled {
		out = append(out, "policy")
	}
	if cur.Order != order {
		out = append(out, "order")
	}
	if cur.Policy.MaxDepth != next.MaxDepth {
		out = append(out, "max_depth")
	}
	if cur.Policy.EditWindowMinutes != next.EditWindowMinutes {
		out = append(out, "edit_window_minutes")
	}
	if cur.Policy.SlowModeSeconds != next.SlowModeSeconds {
		out = append(out, "slow_mode_seconds")
	}
	return out
}
