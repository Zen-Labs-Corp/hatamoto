package admin

import (
	"errors"
	"net/http"
	"slices"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/sdk"
)

// handleCommentPolicyRevoke removes comments.moderate from the plugin that
// moderates this content type. The plugin keeps its other grants and keeps
// running. It is a plugin permission change, so it needs plugin.manage too.
func (a *Admin) handleCommentPolicyRevoke(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionCommentSettingsManage) || !a.requireCan(w, r, authz.ActionPluginManage) {
		return
	}
	svc := a.deps.Comments
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
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
	i := slices.IndexFunc(cfgs, func(c SchemaCommentConfig) bool { return c.ID == id })
	if i < 0 {
		a.notFound(w, r, "Content type", a.csetPath(), "Back to comments")
		return
	}
	typ := cfgs[i]
	holder, found := a.cpolModerators()[typ.Slug]
	if !found {
		a.notFound(w, r, "Moderating plugin", a.csetPath(), "Back to comments")
		return
	}
	// Grant refuses a permission the manifest no longer asks for, so drop those too.
	keep := slices.DeleteFunc(slices.Clone(holder.Granted), func(p string) bool {
		return p == string(sdk.PermCommentsModerate) || !slices.Contains(holder.Permissions, p)
	})
	switch err := pa.Grant(ctx, holder.Name, keep); {
	case errors.Is(err, ErrPluginNotFound):
		a.notFound(w, r, "Moderating plugin", a.csetPath(), "Back to comments")
		return
	case err != nil:
		a.serverError(w, r, "The permission could not be removed", err)
		return
	}
	a.audit(r, "comments.moderation-revoke", id, map[string]any{"plugin": holder.Name, "type": typ.Slug})
	a.SetFlash(w, r, "success", holder.Name+" can no longer moderate comments on "+typ.Name+".")
	Redirect(w, r, a.csetPath()+"#comments-type-"+typ.Slug)
}
