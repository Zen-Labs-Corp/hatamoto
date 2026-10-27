package admin

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/user"
)

const usersPerPage = 25

type usersData struct {
	Table       ui.Table
	Invites     ui.Table
	HasInvites  bool
	InviteCount int
	InviteForm  struct {
		Email, Role, Error string
	}
	Roles  []ui.Option
	Secret *ui.Secret
	// SecretGone: the URL carried a secret handle that is used up, expired or not the user's.
	SecretGone bool
}

// linkData is a one-time link shown to the owner (invite, password reset).
type linkData struct {
	Heading, Intro, URL, Back, BackLabel string
	Emailed                              bool
	NoAddress                            bool // email is on but the site address is not set
}

// Secret is the link as the shared one-time banner shows it.
func (l *linkData) Secret() ui.Secret {
	return ui.Secret{Heading: l.Heading, Note: l.Intro, Value: l.URL, DismissHref: l.Back}
}

type userForm struct {
	ID, Email, Name, Role, Password string
	New                             bool
	Errors                          map[string]string
	Summary                         []ui.FieldError
	Roles                           []ui.Option
	// Created and LastSignIn are the muted meta line of an existing user.
	Created, LastSignIn string
}

func (a *Admin) mountUsers(authed *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	owner.HandleFunc("GET /settings/users", a.handleUsers)
	owner.HandleFunc("GET /settings/users/invite", a.handleInviteNew)
	owner.HandleFunc("POST /settings/users/invite", a.handleInvite)
	owner.HandleFunc("GET /settings/users/invites/{id}/revoke", a.handleInviteRevokeConfirm)
	owner.HandleFunc("POST /settings/users/invites/{id}/revoke", a.handleInviteRevoke)
	owner.HandleFunc("POST /settings/users/invites/{id}/resend", a.handleInviteResend)
	owner.HandleFunc("GET /settings/users/new", a.handleUserNew)
	owner.HandleFunc("POST /settings/users", a.handleUserCreate)
	owner.HandleFunc("GET /settings/users/{id}", a.handleUserEdit)
	owner.HandleFunc("POST /settings/users/{id}", a.handleUserUpdate)
	owner.HandleFunc("POST /settings/users/{id}/reset", a.handleUserReset)
	owner.HandleFunc("GET /settings/users/{id}/delete", a.handleUserDeleteForm)
	owner.HandleFunc("POST /settings/users/{id}/delete", a.handleUserDelete)
}

func roleOptions(selected string) []ui.Option {
	return []ui.Option{
		{Value: "editor", Label: "Editor: manages content", Selected: selected == "editor" || selected == ""},
		{Value: "owner", Label: "Owner: manages content, users and settings", Selected: selected == "owner"},
	}
}

// roleBadgeKind gives each role its own badge colour so a list reads at a glance.
func roleBadgeKind(role string) string {
	switch role {
	case "owner":
		return "info"
	case "admin":
		return "warning"
	}
	return "neutral"
}

func displayName(u *user.User) string {
	if u.Name != nil && *u.Name != "" {
		return *u.Name
	}
	return u.Email
}

func lastSignIn(u *user.User) string {
	if u.LastLoginAt == nil {
		return "Never"
	}
	return fmtDate(*u.LastLoginAt)
}

// lastSignInCell shows the date, or a muted "Never" for someone who has not signed in.
func lastSignInCell(u *user.User) ui.Cell {
	if u.LastLoginAt == nil {
		return ui.Cell{Suffix: lastSignIn(u)}
	}
	return ui.Cell{Text: lastSignIn(u)}
}

func (a *Admin) loadUsers(r *http.Request) ([]*user.User, error) {
	if a.deps.Manager == nil {
		return nil, errors.New("users are not available")
	}
	return a.deps.Manager.List(r.Context(), 1000, 0)
}

func (a *Admin) usersTable(r *http.Request, all []*user.User, self string) ui.Table {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	roleFilter := r.URL.Query().Get("role")
	sortKey, dir := r.URL.Query().Get("sort"), r.URL.Query().Get("dir")
	if sortKey == "" {
		sortKey, dir = "name", "asc"
	}
	if dir != "desc" {
		dir = "asc"
	}
	var rows []*user.User
	for _, u := range all {
		if roleFilter != "" && string(u.Role) != roleFilter {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(displayName(u)+" "+u.Email), strings.ToLower(q)) {
			continue
		}
		rows = append(rows, u)
	}
	less := func(i, j int) bool {
		x, y := rows[i], rows[j]
		var c int
		switch sortKey {
		case "email":
			c = strings.Compare(strings.ToLower(x.Email), strings.ToLower(y.Email))
		case "role":
			c = strings.Compare(string(x.Role), string(y.Role))
		case "last":
			xt, yt := time.Time{}, time.Time{}
			if x.LastLoginAt != nil {
				xt = *x.LastLoginAt
			}
			if y.LastLoginAt != nil {
				yt = *y.LastLoginAt
			}
			c = xt.Compare(yt)
		default:
			c = strings.Compare(strings.ToLower(displayName(x)), strings.ToLower(displayName(y)))
		}
		if c == 0 {
			c = strings.Compare(x.Email, y.Email)
		}
		if dir == "desc" {
			return c > 0
		}
		return c < 0
	}
	sort.SliceStable(rows, less)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	total := len(rows)
	if max := (total + usersPerPage - 1) / usersPerPage; max > 0 && page > max {
		page = max
	}
	start := (page - 1) * usersPerPage
	end := start + usersPerPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if roleFilter != "" {
		query.Set("role", roleFilter)
	}
	if sortKey != "name" || dir != "asc" {
		query.Set("sort", sortKey)
		query.Set("dir", dir)
	}
	t := ui.Table{
		ID: "users", Caption: "Users", BaseURL: a.adminPath + "/settings/users", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name", Sortable: true}, {Key: "email", Label: "Email", Sortable: true}, {Key: "role", Label: "Role", Sortable: true}, {Key: "last", Label: "Last sign-in", Sortable: true}},
		SortKey: sortKey, SortDir: dir, Query: q, SearchLabel: "Search users",
		Filters: []ui.Filter{{Name: "role", Label: "Role", Options: []ui.Option{
			{Value: "", Label: "All roles", Selected: roleFilter == ""},
			{Value: "owner", Label: "Owners", Selected: roleFilter == "owner"},
			{Value: "editor", Label: "Editors", Selected: roleFilter == "editor"},
		}}},
		Page:  ui.Pagination{Page: page, PerPage: usersPerPage, Total: total, BaseURL: a.adminPath + "/settings/users", Query: query},
		Empty: ui.Empty{Icon: "users", Title: "No users match", Body: "Try another search or filter."},
	}
	for _, u := range rows[start:end] {
		you := ""
		if u.ID.String() == self {
			you = "(you)"
		}
		row := ui.Row{ID: u.ID.String(), Label: displayName(u), Cells: []ui.Cell{
			{Text: displayName(u), Link: a.adminPath + "/settings/users/" + u.ID.String(), Initials: initialsOf(displayName(u)), Suffix: you},
			{Text: u.Email},
			{Badge: &ui.Badge{Label: strings.ToUpper(string(u.Role)[:1]) + string(u.Role)[1:], Kind: roleBadgeKind(string(u.Role))}},
			lastSignInCell(u),
		}, Actions: []ui.Action{
			{Label: "Edit", Href: a.adminPath + "/settings/users/" + u.ID.String()},
			{Label: "Reset password", Post: a.adminPath + "/settings/users/" + u.ID.String() + "/reset"},
		}}
		if u.ID.String() != self {
			row.Actions = append(row.Actions, ui.Action{Label: "Delete", Href: a.adminPath + "/settings/users/" + u.ID.String() + "/delete", Post: a.adminPath + "/settings/users/" + u.ID.String() + "/delete",
				Danger: true, ConfirmLabel: "Delete user", Confirm: displayName(u) + " (" + u.Email + ") will no longer be able to sign in. Their content stays."})
		}
		t.Rows = append(t.Rows, row)
	}
	if page == 1 {
		t.CreatedID = createdID(r)
		t.Rows = pinFirst(t.Rows, t.CreatedID)
	}
	return t
}

func (a *Admin) invitesTable(r *http.Request) (ui.Table, bool) {
	t := ui.Table{
		ID: "invites", Caption: "Pending invitations", BaseURL: a.adminPath + "/settings/users", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "email", Label: "Email"}, {Key: "role", Label: "Role"}, {Key: "expires", Label: "Expires"}},
	}
	if a.deps.Tokens == nil {
		return t, false
	}
	invs, err := a.deps.Tokens.ListInvites(r.Context())
	if err != nil {
		a.logger().Error("list invites failed", "err", err)
		t.Error = "The pending invitations could not be loaded."
		return t, true
	}
	for _, inv := range invs {
		id := inv.ID.String()
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: inv.Email, Cells: []ui.Cell{
			{Text: inv.Email}, {Text: string(inv.Role)}, {Text: fmtDate(inv.ExpiresAt)},
		}, Actions: []ui.Action{
			{Label: "Send a new link", Post: a.adminPath + "/settings/users/invites/" + id + "/resend"},
			{Label: "Revoke", Href: a.adminPath + "/settings/users/invites/" + id + "/revoke", Post: a.adminPath + "/settings/users/invites/" + id + "/revoke", Danger: true, ConfirmLabel: "Revoke invitation",
				Confirm: "The link sent to " + inv.Email + " stops working. You can invite them again later."},
		}})
	}
	t.CreatedID = createdID(r)
	t.Rows = pinFirst(t.Rows, t.CreatedID)
	return t, len(invs) > 0 || t.Error != ""
}

func (a *Admin) renderUsers(w http.ResponseWriter, r *http.Request, status int, d usersData) {
	all, err := a.loadUsers(r)
	if err != nil {
		a.logger().Error("list users failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The users could not be loaded.")
		return
	}
	self := ""
	if u := userFrom(r.Context()); u != nil {
		self = u.ID
	}
	d.Table = a.usersTable(r, all, self)
	d.Invites, d.HasInvites = a.invitesTable(r)
	d.InviteCount = len(d.Invites.Rows)
	d.Roles = roleOptions(d.InviteForm.Role)
	v := a.NewView(w, r, "Users")
	v.Data = d
	a.Render(w, r, status, "users/list", v)
}

func (a *Admin) handleUsers(w http.ResponseWriter, r *http.Request) {
	d := usersData{}
	if id := r.URL.Query().Get("secret"); id != "" {
		if e, ok := a.secrets.take(userFrom(r.Context()).ID, id); ok {
			b := e.banner(e.name, a.adminPath+"/settings/users")
			d.Secret = &b
		} else {
			d.SecretGone = true
		}
	}
	a.renderUsers(w, r, http.StatusOK, d)
}

// renderInvite is the no-JS page behind the invite dialog; a failed POST lands here too.
func (a *Admin) renderInvite(w http.ResponseWriter, r *http.Request, status int, d usersData) {
	d.Roles = roleOptions(d.InviteForm.Role)
	v := a.NewView(w, r, "Invite someone")
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "users/invite", v)
}

func (a *Admin) handleInviteNew(w http.ResponseWriter, r *http.Request) {
	a.renderInvite(w, r, http.StatusOK, usersData{})
}

// shownLink builds a one-time link for the owner to copy: from the configured
// site address when there is one, else from the owner's own request.
func (a *Admin) shownLink(r *http.Request, path, token string) string {
	base := a.siteAddress(r)
	if base == "" {
		base = a.requestOrigin(r)
	}
	return base + path + "?token=" + token
}

// issueInvite creates the invite, emails it when email works, parks the link
// in the secret store (essential when SMTP is not configured) and redirects to
// the users list, which shows it once in a banner. flash is the success message.
func (a *Admin) issueInvite(w http.ResponseWriter, r *http.Request, addr string, role user.Role, flash string) {
	self := userFrom(r.Context())
	selfID, _ := uuid.Parse(self.ID)
	token, inv, err := a.deps.Tokens.IssueInvite(r.Context(), addr, role, selfID)
	d := usersData{}
	d.InviteForm.Email, d.InviteForm.Role = addr, string(role)
	switch {
	case errors.Is(err, user.ErrEmailAlreadyExists):
		d.InviteForm.Error = "Someone with that email already has an account."
		a.renderInvite(w, r, http.StatusUnprocessableEntity, d)
		return
	case errors.Is(err, auth.ErrInvalidEmail):
		d.InviteForm.Error = "Enter a valid email address."
		a.renderInvite(w, r, http.StatusUnprocessableEntity, d)
		return
	case errors.Is(err, user.ErrInvalidRole):
		d.InviteForm.Error = "Choose a role."
		a.renderInvite(w, r, http.StatusUnprocessableEntity, d)
		return
	case err != nil:
		a.logger().Error("issue invite failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The invitation could not be created.")
		return
	}
	link := a.shownLink(r, a.adminPath+"/accept", token)
	emailed, noAddress := false, false
	if a.mailConfigured() && a.siteAddress(r) == "" {
		noAddress = true
		a.logger().Warn("invitation not emailed: no site address configured (set PUBLIC_BASE_URL or Settings > Site)")
	} else if a.mailConfigured() {
		inviter := self.Name
		if err := a.deps.Mailer.SendInvite(r.Context(), inv.Email, email.InviteMailData{
			SiteName: a.siteName(r.Context()), InviterName: inviter, Role: string(inv.Role), URL: link, ExpiresIn: "7 days",
		}); err != nil {
			a.logger().Error("send invite failed", "err", err)
		} else {
			emailed = true
		}
	}
	note := "Send this link to " + inv.Email + ". It works once and expires in 7 days. It is shown only now. "
	switch {
	case emailed:
		note += "We also emailed it."
	case noAddress:
		note += "The site address is not set, so nothing was emailed. Set it under Settings > Site (or PUBLIC_BASE_URL), or copy the link and send it yourself."
	default:
		note += "Email is not set up on this site (Settings > Email), so nothing was sent. Copy the link and send it yourself."
	}
	secret := a.secrets.putEntry(secretEntry{user: self.ID, name: "Invitation link for " + inv.Email, key: link, kind: "invite", note: note})
	a.SetFlash(w, r, "success", flash)
	Redirect(w, r, createdRedirect(a.adminPath+"/settings/users", inv.ID.String())+"&secret="+secret)
}

func (a *Admin) renderLink(w http.ResponseWriter, r *http.Request, l *linkData) {
	v := a.NewView(w, r, l.Heading)
	v.Data = l
	a.Render(w, r, http.StatusOK, "users/link", v)
}

func (a *Admin) handleInvite(w http.ResponseWriter, r *http.Request) {
	role, err := user.ParseRole(r.PostFormValue("role"))
	if err != nil {
		role = user.RoleEditor
	}
	if a.deps.Tokens == nil {
		a.Error(w, r, http.StatusServiceUnavailable, "Invitations unavailable", "The server is not ready.")
		return
	}
	a.issueInvite(w, r, r.PostFormValue("email"), role, "Invite created")
}

// handleInviteRevokeConfirm is the no-JS page behind the revoke dialog.
func (a *Admin) handleInviteRevokeConfirm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || a.deps.Tokens == nil {
		a.Error(w, r, http.StatusNotFound, "Not found", "That invitation does not exist.")
		return
	}
	invs, err := a.deps.Tokens.ListInvites(r.Context())
	if err != nil {
		a.logger().Error("list invites failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The invitations could not be loaded.")
		return
	}
	for _, inv := range invs {
		if inv.ID == id {
			a.renderConfirm(w, r, confirmData{
				Title: "Revoke the invitation for " + inv.Email + "?", Message: "The link sent to " + inv.Email + " stops working. You can invite them again later.",
				Post: a.adminPath + "/settings/users/invites/" + id.String() + "/revoke", Label: "Revoke invitation", Back: a.adminPath + "/settings/users", Danger: true,
			})
			return
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Not found", "That invitation does not exist.", a.adminPath+"/settings/users", "Back to users")
}

func (a *Admin) handleInviteRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || a.deps.Tokens == nil {
		a.Error(w, r, http.StatusNotFound, "Not found", "That invitation does not exist.")
		return
	}
	if err := a.deps.Tokens.RevokeInvite(r.Context(), id); err != nil {
		a.logger().Error("revoke invite failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The invitation could not be revoked.")
		return
	}
	a.SetFlash(w, r, "success", "Invitation revoked.")
	Redirect(w, r, a.adminPath+"/settings/users")
}

func (a *Admin) handleInviteResend(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || a.deps.Tokens == nil {
		a.Error(w, r, http.StatusNotFound, "Not found", "That invitation does not exist.")
		return
	}
	invs, err := a.deps.Tokens.ListInvites(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The invitation could not be loaded.")
		return
	}
	for _, inv := range invs {
		if inv.ID == id {
			a.issueInvite(w, r, inv.Email, inv.Role, "Invite sent again")
			return
		}
	}
	a.Error(w, r, http.StatusNotFound, "Not found", "That invitation no longer exists.")
}

func (a *Admin) renderUserForm(w http.ResponseWriter, r *http.Request, status int, f userForm) {
	title := "Edit user"
	if f.New {
		title = "Add a user with a password"
	}
	f.Roles = roleOptions(f.Role)
	f.Summary = nil
	for _, k := range []struct{ id, label string }{{"email", "Email"}, {"name", "Name"}, {"role", "Role"}, {"password", "Password"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, title)
	v.Data = f
	v.Width = "narrow"
	a.Render(w, r, status, "users/edit", v)
}

func (a *Admin) handleUserNew(w http.ResponseWriter, r *http.Request) {
	// The invite form's "Add with a password instead" link carries the address typed there.
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if len(email) > 254 || !strings.Contains(email, "@") || strings.ContainsAny(email, " <>") {
		email = ""
	}
	a.renderUserForm(w, r, http.StatusOK, userForm{New: true, Role: "editor", Email: email})
}

func (a *Admin) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	f := userForm{New: true, Email: strings.ToLower(strings.TrimSpace(r.PostFormValue("email"))), Name: strings.TrimSpace(r.PostFormValue("name")), Role: r.PostFormValue("role"), Errors: map[string]string{}}
	pw := r.PostFormValue("password")
	role, err := user.ParseRole(f.Role)
	if err != nil {
		f.Errors["role"] = "Choose a role"
	}
	if !strings.Contains(f.Email, "@") || strings.ContainsAny(f.Email, " <>") {
		f.Errors["email"] = "Enter a valid email address"
	}
	if len(pw) < auth.MinPasswordLen {
		f.Errors["password"] = sentence(auth.ErrPasswordShort.Error())
	}
	if len(f.Errors) == 0 && a.deps.Manager != nil {
		hash, herr := auth.HashPasswordContext(r.Context(), pw)
		if herr != nil {
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The user could not be created.")
			return
		}
		_, cerr := a.deps.Manager.Create(r.Context(), &user.CreateUserInput{Email: f.Email, Role: role, Name: f.Name}, hash)
		switch {
		case errors.Is(cerr, user.ErrEmailAlreadyExists):
			f.Errors["email"] = "Someone with that email already has an account"
		case cerr != nil:
			a.logger().Error("create user failed", "err", cerr)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The user could not be created.")
			return
		default:
			a.SetFlash(w, r, "success", "User added.")
			http.Redirect(w, r, a.adminPath+"/settings/users", http.StatusSeeOther)
			return
		}
	}
	a.renderUserForm(w, r, http.StatusUnprocessableEntity, f)
}

func (a *Admin) targetUser(w http.ResponseWriter, r *http.Request) (*user.User, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil && a.deps.Manager != nil {
		if u, gerr := a.deps.Manager.GetByID(r.Context(), id); gerr == nil {
			return u, true
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "User not found", "That user does not exist.", a.adminPath+"/settings/users", "Back to users")
	return nil, false
}

func (a *Admin) handleUserEdit(w http.ResponseWriter, r *http.Request) {
	u, ok := a.targetUser(w, r)
	if !ok {
		return
	}
	name := ""
	if u.Name != nil {
		name = *u.Name
	}
	a.renderUserForm(w, r, http.StatusOK, userForm{ID: u.ID.String(), Email: u.Email, Name: name, Role: string(u.Role), Created: fmtDate(u.CreatedAt), LastSignIn: lastSignIn(u)})
}

func (a *Admin) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	u, ok := a.targetUser(w, r)
	if !ok {
		return
	}
	f := userForm{ID: u.ID.String(), Email: u.Email, Name: strings.TrimSpace(r.PostFormValue("name")), Role: r.PostFormValue("role"), Errors: map[string]string{}}
	role, err := user.ParseRole(f.Role)
	if err != nil {
		f.Errors["role"] = "Choose a role"
		a.renderUserForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	_, uerr := a.deps.Manager.Update(r.Context(), u.ID, &user.UpdateUserInput{Name: &f.Name, Role: &role})
	switch {
	case errors.Is(uerr, user.ErrLastOwner):
		f.Errors["role"] = "There must be at least one owner. Make someone else an owner first."
		a.renderUserForm(w, r, http.StatusUnprocessableEntity, f)
		return
	case uerr != nil:
		a.logger().Error("update user failed", "err", uerr)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The user could not be saved.")
		return
	}
	// A changed role takes effect on the next request (sessions look the user up),
	// so there is nothing to revoke here.
	a.SetFlash(w, r, "success", "User saved.")
	http.Redirect(w, r, a.adminPath+"/settings/users", http.StatusSeeOther)
}

func (a *Admin) handleUserReset(w http.ResponseWriter, r *http.Request) {
	u, ok := a.targetUser(w, r)
	if !ok {
		return
	}
	if a.deps.Tokens == nil {
		a.Error(w, r, http.StatusServiceUnavailable, "Unavailable", "The server is not ready.")
		return
	}
	token, err := a.deps.Tokens.IssueReset(r.Context(), u.ID)
	if err != nil {
		if errors.Is(err, auth.ErrResetThrottle) {
			a.Error(w, r, http.StatusTooManyRequests, "Too many requests", "A reset link was already requested three times this hour for this user.")
			return
		}
		a.logger().Error("issue reset failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The reset link could not be created.")
		return
	}
	link := a.shownLink(r, a.adminPath+"/reset", token)
	emailed, noAddress := false, false
	if a.mailConfigured() && a.siteAddress(r) == "" {
		noAddress = true
		a.logger().Warn("password reset not emailed: no site address configured (set PUBLIC_BASE_URL or Settings > Site)")
	} else if a.mailConfigured() {
		if err := a.deps.Mailer.SendPasswordReset(r.Context(), u.Email, email.ResetMailData{SiteName: a.siteName(r.Context()), URL: link, ExpiresIn: "1 hour"}); err != nil {
			a.logger().Error("send reset failed", "err", err)
		} else {
			emailed = true
		}
	}
	a.renderLink(w, r, &linkData{
		Heading: "Password reset link for " + displayName(u),
		Intro:   "Send this link to " + u.Email + ". It works once and expires in an hour. It is shown only now.",
		URL:     link, Back: a.adminPath + "/settings/users", BackLabel: "Back to users", Emailed: emailed, NoAddress: noAddress,
	})
}

type deleteData struct {
	ID, Name, Email string
	Blocked         string
}

func (a *Admin) renderDelete(w http.ResponseWriter, r *http.Request, status int, u *user.User, blocked string) {
	v := a.NewView(w, r, "Delete "+displayName(u))
	v.Data = deleteData{ID: u.ID.String(), Name: displayName(u), Email: u.Email, Blocked: blocked}
	v.Width = "narrow"
	a.Render(w, r, status, "users/delete", v)
}

func (a *Admin) handleUserDeleteForm(w http.ResponseWriter, r *http.Request) {
	u, ok := a.targetUser(w, r)
	if !ok {
		return
	}
	blocked := ""
	if cu := userFrom(r.Context()); cu != nil && cu.ID == u.ID.String() {
		blocked = "You cannot delete your own account."
	}
	a.renderDelete(w, r, http.StatusOK, u, blocked)
}

func (a *Admin) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := a.targetUser(w, r)
	if !ok {
		return
	}
	if cu := userFrom(r.Context()); cu != nil && cu.ID == u.ID.String() {
		a.renderDelete(w, r, http.StatusUnprocessableEntity, u, "You cannot delete your own account.")
		return
	}
	err := a.deps.Manager.Delete(r.Context(), u.ID)
	switch {
	case errors.Is(err, user.ErrLastOwner):
		a.renderDelete(w, r, http.StatusUnprocessableEntity, u, "This is the only owner. Make someone else an owner first.")
		return
	case err != nil && !errors.Is(err, user.ErrUserNotFound):
		a.logger().Error("delete user failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The user could not be deleted.")
		return
	}
	if a.deps.Sessions != nil {
		_ = a.deps.Sessions.RevokeAllForUser(r.Context(), u.ID)
	}
	a.SetFlash(w, r, "success", displayName(u)+" was deleted.")
	http.Redirect(w, r, a.adminPath+"/settings/users", http.StatusSeeOther)
}
