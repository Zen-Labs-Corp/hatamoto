package admin

import (
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/user"
)

type acceptData struct {
	Token, Email, Role, Name string
	Valid                    bool
	Exists                   bool
	Error, Match             string
}

func (a *Admin) mountAccept(public *httpx.Group) {
	public.HandleFunc("GET /accept", a.handleAcceptForm)
	public.HandleFunc("POST /accept", a.handleAccept)
}

func (a *Admin) handleAcceptForm(w http.ResponseWriter, r *http.Request) {
	noReferrer(w)
	token := r.URL.Query().Get("token")
	d := acceptData{Token: token}
	if a.deps.Tokens != nil {
		if inv, err := a.deps.Tokens.PeekInvite(r.Context(), token); err == nil {
			d.Valid, d.Email, d.Role = true, inv.Email, string(inv.Role)
		}
	}
	v := a.NewView(w, r, "Accept your invitation")
	v.Data = d
	a.Render(w, r, http.StatusOK, "auth/accept", v)
}

func (a *Admin) handleAccept(w http.ResponseWriter, r *http.Request) {
	noReferrer(w)
	token := r.PostFormValue("token")
	name := strings.TrimSpace(r.PostFormValue("name"))
	pw, confirm := r.PostFormValue("password"), r.PostFormValue("confirm")
	render := func(status int, d acceptData) {
		v := a.NewView(w, r, "Accept your invitation")
		v.Data = d
		a.Render(w, r, status, "auth/accept", v)
	}
	if a.deps.Tokens == nil {
		render(http.StatusOK, acceptData{Token: token})
		return
	}
	inv, err := a.deps.Tokens.PeekInvite(r.Context(), token)
	if err != nil {
		render(http.StatusOK, acceptData{Token: token})
		return
	}
	d := acceptData{Token: token, Valid: true, Email: inv.Email, Role: string(inv.Role), Name: name}
	switch {
	case len(pw) < auth.MinPasswordLen:
		d.Error = sentence(auth.ErrPasswordShort.Error())
		render(http.StatusUnprocessableEntity, d)
		return
	case pw != confirm:
		d.Match = "The passwords do not match."
		render(http.StatusUnprocessableEntity, d)
		return
	}
	u, err := a.deps.Tokens.ConsumeInvite(r.Context(), token, name, pw)
	switch {
	case errors.Is(err, user.ErrEmailAlreadyExists):
		render(http.StatusOK, acceptData{Token: token, Exists: true})
		return
	case errors.Is(err, auth.ErrTokenInvalid):
		render(http.StatusOK, acceptData{Token: token})
		return
	case err != nil:
		a.logger().Error("accept invite failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not create your account. Try again.")
		return
	}
	a.signInAfter(w, r, u, "Welcome. Your account is ready.")
}
