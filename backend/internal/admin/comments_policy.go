package admin

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/sdk"
)

// Wording of the community policy warnings. The templates carry the same text
// literally (the design check pins both).
const (
	cpolOnWarning = "Links and first-time commenters are posted at once, not held."
	cpolOffHelp   = "Links and new commenters are held."
	cpolConfirm   = "Are you sure? With the community policy on, comments with links and first comments on this content type are posted at once, without review. Spam checks, blocked commenters, duplicates, captcha and surge mode still hold comments."
	cpolTick      = "I understand comments on this type will not wait for review"
)

// cpolFields are the policy numbers shown while the policy is on.
var cpolFields = []csetField{
	{"max_depth", "Reply depth", "Default 10, max 32.", 1, 32},
	{"edit_window_minutes", "Edit window", "Minutes. Default 15.", 1, 1440},
	{"slow_mode_seconds", "Slow mode", "Seconds between comments by one person. 0 turns it off.", 0, 86400},
}

// cpolType is one content type's card in Settings > Comments > Content types.
type cpolType struct {
	ID, Slug, Name, ModeLabel string
	Supported                 bool // false for a built-in type: shown read-only
	On                        bool
	OrderValue                string
	Order                     []ui.Option
	Numbers                   []csetNumber // only while the policy is on
	OrderError                string
	CanRevoke                 bool   // the viewer may remove the moderator's grant
	ModeratorHref             string // the plugin's Permissions page: the link without script
	Moderator                 string // plugin holding comments.moderate for this type, "" when none
}

// cpolModeLabel is how a type's comment mode reads on its card.
func cpolModeLabel(mode string) string {
	switch mode {
	case "closed":
		return "closed"
	case "hidden":
		return "hidden"
	}
	return "on"
}

// cpolOrderOptions are the order choices. "" follows the site sort; a stored
// "rank" is shown selected so a save keeps it.
func cpolOrderOptions(order, siteSort string, rank bool) []ui.Option {
	site := "Newest first"
	if siteSort == "oldest" {
		site = "Oldest first"
	}
	opts := []ui.Option{
		{Value: "", Label: "Site default (" + site + ")"},
		{Value: "newest", Label: "Newest first"},
		{Value: "oldest", Label: "Oldest first"},
	}
	if rank || order == "rank" {
		opts = append(opts, ui.Option{Value: "rank", Label: "By rank"})
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == order
	}
	return opts
}

// cpolView builds a card. vals, when not nil, are the posted values (after a
// refused save) and errs the messages by field name.
func cpolView(c SchemaCommentConfig, siteSort, moderator string, vals url.Values, errs map[string]string) cpolType {
	t := cpolType{
		ID: c.ID, Slug: c.Slug, Name: c.Name, ModeLabel: cpolModeLabel(c.Mode),
		Supported: !c.Builtin, On: c.Policy.Enabled, OrderValue: c.Order, OrderError: errs["order"], Moderator: moderator,
	}
	if vals != nil {
		t.OrderValue = vals.Get("order")
	}
	t.Order = cpolOrderOptions(t.OrderValue, siteSort, moderator != "")
	if !t.On {
		return t
	}
	stored := map[string]int{
		"max_depth": c.Policy.MaxDepth, "edit_window_minutes": c.Policy.EditWindowMinutes, "slow_mode_seconds": c.Policy.SlowModeSeconds,
	}
	for _, f := range cpolFields {
		v := strconv.Itoa(stored[f.Name])
		if vals != nil && vals.Has(f.Name) {
			v = vals.Get(f.Name)
		}
		t.Numbers = append(t.Numbers, csetNumber{csetField: f, ID: "comments-type-" + c.Slug + "-" + csetFieldID(f.Name)[len("comments-"):], Value: v, Error: errs[f.Name]})
	}
	return t
}

// cpolPosted is a refused post to one content type: its values and the
// messages by field name.
type cpolPosted struct {
	ID   string
	Vals url.Values
	Errs map[string]string
}

// cpolTypes lists the content type cards with one service call. It is empty
// when the store cannot hold a policy. posted, when not nil, fills one card
// with a refused post.
func (a *Admin) cpolTypes(r *http.Request, svc CommentsService, siteSort string, posted *cpolPosted) []cpolType {
	if !svc.Supports("schema-policy") {
		return nil
	}
	cfgs, err := svc.SchemaCommentConfigs(r.Context())
	if err != nil {
		a.logger().Error("comment content types", "err", err)
		return nil
	}
	mods := a.cpolModerators()
	canRevoke := a.can(r.Context(), authz.ActionCommentSettingsManage) && a.can(r.Context(), authz.ActionPluginManage)
	out := make([]cpolType, 0, len(cfgs))
	for _, c := range cfgs {
		t := cpolView(c, siteSort, mods[c.Slug].Name, nil, nil)
		if posted != nil && posted.ID == c.ID {
			t = cpolView(c, siteSort, mods[c.Slug].Name, posted.Vals, posted.Errs)
		}
		t.CanRevoke = canRevoke && t.Moderator != ""
		if t.CanRevoke {
			t.ModeratorHref = a.pluginURL(t.Moderator, "permissions")
		}
		out = append(out, t)
	}
	return out
}

// cpolModerators maps a content type slug to the first plugin that holds
// comments.moderate and declares that type as its entity schema. Grants are
// per plugin; the manifest's entity_schema is the scope.
func (a *Admin) cpolModerators() map[string]PluginInfo {
	out := map[string]PluginInfo{}
	if a.deps.Growth.Plugins == nil {
		return out
	}
	for _, p := range a.deps.Growth.Plugins.Plugins() {
		if p.EntitySchema == "" || !slices.Contains(p.Granted, string(sdk.PermCommentsModerate)) {
			continue
		}
		if _, taken := out[p.EntitySchema]; !taken {
			out[p.EntitySchema] = p
		}
	}
	return out
}
