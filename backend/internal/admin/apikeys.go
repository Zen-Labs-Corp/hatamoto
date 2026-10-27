package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
)

type apiKeysData struct {
	Table   ui.Table
	Scopes  []scopeOption
	Expiry  []ui.Option
	Name    string
	NameErr string
	Summary []ui.FieldError
	Secret  *ui.Secret
	// SecretGone: the URL carried a secret handle that is used up, expired or not the user's.
	SecretGone bool
	Actions    []ui.Button
}

type scopeOption struct {
	Value, Label, Help string
	Checked            bool
}

// scopeHelp says in a line what a scope lets the key do.
var scopeHelp = map[string]string{
	"read:entities":      "Read pages, posts and other content, including drafts.",
	"write:entities":     "Create, edit, publish and delete content.",
	"read:citations":     "Read which AI assistants cite your pages.",
	"read:reports":       "Read analytics and crawler reports.",
	"write:reports":      "Create and change reports.",
	"read:query_suites":  "Read saved sets of questions to test against assistants.",
	"write:query_suites": "Create and change those sets of questions.",
	"write:media":        "Upload and delete files in the media library.",
	"write:menus":        "Change navigation menus.",
	"write:redirects":    "Create and change redirects.",
	"write:terms":        "Create and change taxonomy terms.",
	"*":                  "Everything above, and anything added later. Give it only to keys you fully trust.",
}

func (a *Admin) mountAPIKeys(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/api-keys", a.handleAPIKeys)
	owner.HandleFunc("POST /settings/api-keys", a.handleAPIKeyCreate)
	owner.HandleFunc("GET /settings/api-keys/new", a.handleAPIKeyNew)
	owner.HandleFunc("GET /settings/api-keys/secret", a.handleAPIKeySecret)
	owner.HandleFunc("GET /settings/api-keys/{id}/{action}", a.handleAPIKeyConfirm)
	owner.HandleFunc("POST /settings/api-keys/{id}/revoke", a.handleAPIKeyRevoke)
	owner.HandleFunc("POST /settings/api-keys/{id}/regenerate", a.handleAPIKeyRegenerate)
	owner.HandleFunc("POST /settings/api-keys/{id}/delete", a.handleAPIKeyDelete)
}

func scopeOptions(checked map[string]bool) []scopeOption {
	var out []scopeOption
	for _, s := range auth.ValidScopes() {
		out = append(out, scopeOption{Value: string(s), Label: scopeLabel(string(s)), Help: scopeHelp[string(s)], Checked: checked[string(s)]})
	}
	return out
}

func scopeLabel(s string) string {
	if s == "*" {
		return "Everything (full access)"
	}
	verb, what, _ := strings.Cut(s, ":")
	return strings.ToUpper(verb[:1]) + verb[1:] + " " + strings.ReplaceAll(what, "_", " ")
}

func expiryOptions() []ui.Option {
	return []ui.Option{{Value: "", Label: "Never", Selected: true}, {Value: "30", Label: "In 30 days"}, {Value: "90", Label: "In 90 days"}, {Value: "365", Label: "In a year"}}
}

func (a *Admin) keysTable(r *http.Request, keys []*auth.APIKey) ui.Table {
	t := ui.Table{
		ID: "api-keys", Caption: "API keys", BaseURL: a.adminPath + "/settings/api-keys", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "key", Label: "Key"}, {Key: "scopes", Label: "Access"}, {Key: "used", Label: "Last used"}, {Key: "status", Label: "Status"}},
		Empty:   ui.Empty{Icon: "key", Title: "No API keys", Body: "Create a key to let a script or integration use the API."},
	}
	now := time.Now()
	for _, k := range keys {
		id := k.ID.String()
		status := ui.Badge{Label: "Active", Kind: "success"}
		switch {
		case k.RevokedAt != nil || !k.IsActive:
			status = ui.Badge{Label: "Revoked", Kind: "danger"}
		case k.ExpiresAt != nil && k.ExpiresAt.Before(now):
			status = ui.Badge{Label: "Expired", Kind: "warning"}
		}
		var scopes []string
		for _, s := range k.Scopes {
			scopes = append(scopes, scopeLabel(string(s)))
		}
		used := "Never"
		if k.LastUsedAt != nil {
			used = fmtDate(*k.LastUsedAt)
		}
		row := ui.Row{ID: id, Label: k.Name, Cells: []ui.Cell{
			{Text: k.Name}, {Text: k.KeyPrefix + "…"}, {Text: strings.Join(scopes, ", ")}, {Text: used}, {Badge: &status},
		}}
		if status.Label == "Active" {
			row.Actions = append(row.Actions,
				ui.Action{Label: "New secret", Href: a.adminPath + "/settings/api-keys/" + id + "/regenerate", Post: a.adminPath + "/settings/api-keys/" + id + "/regenerate", ConfirmLabel: "Create new secret",
					Confirm: "The old secret for " + k.Name + " stops working immediately. Anything using it breaks until you give it the new one."},
				ui.Action{Label: "Revoke", Href: a.adminPath + "/settings/api-keys/" + id + "/revoke", Post: a.adminPath + "/settings/api-keys/" + id + "/revoke", ConfirmLabel: "Revoke key", Danger: true,
					Confirm: k.Name + " stops working immediately. This cannot be undone."})
		}
		row.Actions = append(row.Actions, ui.Action{Label: "Delete", Href: a.adminPath + "/settings/api-keys/" + id + "/delete", Post: a.adminPath + "/settings/api-keys/" + id + "/delete", ConfirmLabel: "Delete key", Danger: true,
			Confirm: "Delete " + k.Name + "? Anything using it stops working. This cannot be undone."})
		t.Rows = append(t.Rows, row)
	}
	return t
}

// keysData fills the API key section: the key table and the create form's
// defaults around whatever the failed submission left in d.
func (a *Admin) keysData(w http.ResponseWriter, r *http.Request, d apiKeysData) (apiKeysData, bool) {
	keys, err := a.deps.APIKeys.List(r.Context())
	if err != nil {
		a.logger().Error("list api keys failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The keys could not be loaded.")
		return d, false
	}
	d.Table = a.keysTable(r, keys)
	if d.Scopes == nil {
		d.Scopes = scopeOptions(map[string]bool{"read:entities": true})
	}
	d.Expiry = expiryOptions()
	return d, true
}

func (a *Admin) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	a.renderKeys(w, r, http.StatusOK, apiKeysData{})
}

// handleAPIKeyNew is the page behind the header's New API key link: what a
// browser without JavaScript sees instead of the dialog.
func (a *Admin) handleAPIKeyNew(w http.ResponseWriter, r *http.Request) {
	a.renderKeyNew(w, r, http.StatusOK, apiKeysData{})
}

// renderKeys is the API settings page: the key table (the just-created key
// first), the one-time secret banner and the create dialog.
func (a *Admin) renderKeys(w http.ResponseWriter, r *http.Request, status int, d apiKeysData) {
	if a.deps.APIKeys == nil {
		a.Error(w, r, http.StatusNotFound, "Not found", "API keys are not available on this server.")
		return
	}
	d, ok := a.keysData(w, r, d)
	if !ok {
		return
	}
	list := a.adminPath + "/settings/api-keys"
	if id := createdID(r); id != "" {
		d.Table.Rows = pinFirst(d.Table.Rows, id)
		d.Table.CreatedID = id
		if e, ok := a.secrets.take(userFrom(r.Context()).ID, r.URL.Query().Get("secret")); ok {
			b := e.banner("Copy your API key", list)
			b.Note = "Copy it now."
			if e.name != "" {
				b.Note = "This is the key for " + e.name + ". Copy it now."
			}
			d.Secret = &b
		} else if r.URL.Query().Get("secret") != "" {
			d.SecretGone = true
		}
	}
	d.Actions = []ui.Button{{Label: "New API key", Variant: "primary", Icon: "plus", Href: list + "/new", Attrs: map[string]string{"data-hm-open": "new-key"}}}
	v := a.NewView(w, r, "API")
	v.Section, v.Tab = "api", "api"
	v.Data = d
	a.Render(w, r, status, "settings/api", v)
}

// renderKeyNew is the standalone create page, also the re-render of a failed
// submission.
func (a *Admin) renderKeyNew(w http.ResponseWriter, r *http.Request, status int, d apiKeysData) {
	if a.deps.APIKeys == nil {
		a.Error(w, r, http.StatusNotFound, "Not found", "API keys are not available on this server.")
		return
	}
	if d.Scopes == nil {
		d.Scopes = scopeOptions(map[string]bool{"read:entities": true})
	}
	d.Expiry = expiryOptions()
	v := a.NewView(w, r, "New API key")
	v.Width = "narrow"
	v.Section, v.Tab = "api", "api"
	v.Crumbs = a.areaCrumbs("api", Crumb{Label: "New API key"}) // the same trail for /new and the 422 re-render
	v.Data = d
	a.Render(w, r, status, "settings/apikeynew", v)
}

func (a *Admin) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	checked := map[string]bool{}
	var scopes []auth.APIKeyScope
	for _, s := range r.PostForm["scopes"] {
		if auth.IsValidScope(s) {
			checked[s] = true
			scopes = append(scopes, auth.APIKeyScope(s))
		}
	}
	d := apiKeysData{Name: name, Scopes: scopeOptions(checked)}
	if name == "" {
		d.NameErr = "Enter a name"
		d.Summary = append(d.Summary, ui.FieldError{ID: "key-name", Label: "Name", Message: d.NameErr})
	}
	if len(scopes) == 0 {
		d.Summary = append(d.Summary, ui.FieldError{ID: "scope-0", Label: "Access", Message: "Choose at least one"})
	}
	if len(d.Summary) > 0 {
		a.renderKeyNew(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	in := &auth.CreateAPIKeyInput{Name: name, Scopes: scopes}
	switch r.PostFormValue("expiry") {
	case "30", "90", "365":
		days := map[string]int{"30": 30, "90": 90, "365": 365}[r.PostFormValue("expiry")]
		exp := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		in.ExpiresAt = &exp
	}
	self, _ := uuid.Parse(userFrom(r.Context()).ID)
	key, err := a.deps.APIKeys.Create(r.Context(), &self, in)
	if err != nil {
		a.logger().Error("create api key failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The key could not be created.")
		return
	}
	a.SetFlash(w, r, "success", "API key created.")
	a.redirectToSecret(w, r, key.ID.String(), name, key.PlainKey)
}

// redirectToSecret parks the one-time secret and sends the browser to the key
// list, which shows it once (POST, redirect, GET: reloading cannot mint another
// key). id is the key's ID, so the list can pin its row.
func (a *Admin) redirectToSecret(w http.ResponseWriter, r *http.Request, id, name, key string) {
	token := a.secrets.put(userFrom(r.Context()).ID, name, key)
	http.Redirect(w, r, createdRedirect(a.adminPath+"/settings/api-keys", id)+"&secret="+token, http.StatusSeeOther)
}

// handleAPIKeySecret keeps the old secret URL working: the secret now shows on the list.
func (a *Admin) handleAPIKeySecret(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, a.adminPath+"/settings/api-keys", http.StatusSeeOther)
}

// handleAPIKeyConfirm is the page behind the key actions' confirmation dialog:
// what a browser without JavaScript sees.
func (a *Admin) handleAPIKeyConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := a.keyID(w, r)
	if !ok {
		return
	}
	keys, err := a.deps.APIKeys.List(r.Context())
	if err != nil {
		a.logger().Error("list api keys failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The keys could not be loaded.")
		return
	}
	for _, k := range keys {
		if k.ID != id {
			continue
		}
		d := confirmData{Post: a.adminPath + "/settings/api-keys/" + id.String() + "/" + r.PathValue("action"), Back: a.adminPath + "/settings/api-keys", Danger: true}
		switch r.PathValue("action") {
		case "revoke":
			d.Title, d.Label, d.Message = "Revoke "+k.Name+"?", "Revoke key", k.Name+" stops working immediately. This cannot be undone."
		case "delete":
			d.Title, d.Label, d.Message = "Delete "+k.Name+"?", "Delete key", "Anything using "+k.Name+" stops working. This cannot be undone."
		case "regenerate":
			d.Title, d.Label, d.Danger = "Create a new secret for "+k.Name+"?", "Create new secret", false
			d.Message = "The old secret stops working immediately. Anything using it breaks until you give it the new one."
		default:
			a.Error(w, r, http.StatusNotFound, "Not found", "That page does not exist.")
			return
		}
		a.renderConfirm(w, r, d)
		return
	}
	a.Error(w, r, http.StatusNotFound, "Not found", "That key does not exist.")
}

func (a *Admin) keyID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || a.deps.APIKeys == nil {
		a.Error(w, r, http.StatusNotFound, "Not found", "That key does not exist.")
		return uuid.Nil, false
	}
	return id, true
}

func (a *Admin) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	id, ok := a.keyID(w, r)
	if !ok {
		return
	}
	self, _ := uuid.Parse(userFrom(r.Context()).ID)
	if err := a.deps.APIKeys.Revoke(r.Context(), id, &self); err != nil {
		a.logger().Error("revoke api key failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The key could not be revoked.")
		return
	}
	a.SetFlash(w, r, "success", "Key revoked. It stops working immediately.")
	http.Redirect(w, r, a.adminPath+"/settings/api-keys", http.StatusSeeOther)
}

func (a *Admin) handleAPIKeyRegenerate(w http.ResponseWriter, r *http.Request) {
	id, ok := a.keyID(w, r)
	if !ok {
		return
	}
	key, err := a.deps.APIKeys.Regenerate(r.Context(), id)
	if err != nil {
		a.logger().Error("regenerate api key failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "A new secret could not be created.")
		return
	}
	a.redirectToSecret(w, r, id.String(), "", key)
}

func (a *Admin) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := a.keyID(w, r)
	if !ok {
		return
	}
	if err := a.deps.APIKeys.Delete(r.Context(), id); err != nil {
		a.logger().Error("delete api key failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The key could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", "Key deleted.")
	http.Redirect(w, r, a.adminPath+"/settings/api-keys", http.StatusSeeOther)
}
