package admin

import (
	"net/http"
	"net/url"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
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
func cpolOrderOptions(order, siteSort string) []ui.Option {
	site := "Newest first"
	if siteSort == "oldest" {
		site = "Oldest first"
	}
	opts := []ui.Option{
		{Value: "", Label: "Site default (" + site + ")"},
		{Value: "newest", Label: "Newest first"},
		{Value: "oldest", Label: "Oldest first"},
	}
	if order == "rank" {
		opts = append(opts, ui.Option{Value: "rank", Label: "By rank"})
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == order
	}
	return opts
}

// cpolView builds a card. vals, when not nil, are the posted values (after a
// refused save) and errs the messages by field name.
func cpolView(c SchemaCommentConfig, siteSort string, vals url.Values, errs map[string]string) cpolType {
	t := cpolType{
		ID: c.ID, Slug: c.Slug, Name: c.Name, ModeLabel: cpolModeLabel(c.Mode),
		Supported: !c.Builtin, On: c.Policy.Enabled, OrderValue: c.Order, OrderError: errs["order"],
	}
	if vals != nil {
		t.OrderValue = vals.Get("order")
	}
	t.Order = cpolOrderOptions(t.OrderValue, siteSort)
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

// cpolTypes lists the content type cards with one service call. It is empty
// when the store cannot hold a policy.
func (a *Admin) cpolTypes(r *http.Request, svc CommentsService, siteSort string) []cpolType {
	if !svc.Supports("schema-policy") {
		return nil
	}
	cfgs, err := svc.SchemaCommentConfigs(r.Context())
	if err != nil {
		a.logger().Error("comment content types", "err", err)
		return nil
	}
	out := make([]cpolType, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, cpolView(c, siteSort, nil, nil))
	}
	return out
}
