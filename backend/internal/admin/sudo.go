package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/session"
)

// sudoMessage is the only thing a failed confirmation says: a wrong password, a
// wrong code and a locked account all look the same.
const sudoMessage = "That did not work. Check your details and try again."

// sudoUnavailableMessage is shown when the second-factor provider could not decide.
const sudoUnavailableMessage = "We could not confirm that right now. Try again in a moment."

// errSudoProvider marks a Verify error that is not a wrong code.
var errSudoProvider = errors.New("sudo: second factor provider failed")

// passwordConfirmer is what *auth.Service offers for sudo mode. The admin reads
// it off Deps.Auth with a type assertion so Authenticator stays as it is.
type passwordConfirmer interface {
	ConfirmPassword(ctx context.Context, userID uuid.UUID, email, password string) error
}

type sudoData struct {
	Email string
	// Action and Home are the form target and the cancel link.
	Action, Home string
	// Next is the validated place to return to after confirming.
	Next string
	// HasSecondFactor shows the code field.
	HasSecondFactor bool
	Status          *Flash
	// Resubmit is set when the user came here from a blocked POST. It rides in a
	// hidden field so the reminder survives a wrong password.
	Resubmit bool
	// Notice is the resubmit reminder shown next to an error Status.
	Notice *Flash
}

// HasStatus tells the template to tie the inputs to the status line with aria-describedby.
func (d sudoData) HasStatus() bool { return d.Status != nil }

func (a *Admin) mountSudo(authed *httpx.Group) {
	g := authed
	if a.deps.RateLimit != nil {
		g = authed.Group("", a.deps.RateLimit)
	}
	g.HandleFunc("GET /sudo", a.handleSudoPage)
	g.HandleFunc("POST /sudo", a.handleSudo)
}

// sudoView is the page's data for the signed-in user. status may be nil.
func (a *Admin) sudoView(w http.ResponseWriter, r *http.Request, status *Flash) *View {
	d := sudoData{Action: a.sudoPath(), Home: a.url(), Next: SafeNextFor(a.adminPath, r.FormValue("next")), Status: status}
	if u := userFrom(r.Context()); u != nil {
		d.Email = u.Email
	}
	if s := sessionFrom(r.Context()); s != nil && s.UserID != nil {
		if f := a.sudo.secondFactor(); f != nil {
			// An error shows the field too: the POST refuses until the provider answers.
			has, err := f.Has(r.Context(), *s.UserID)
			d.HasSecondFactor = has || err != nil
		}
	}
	if r.FormValue("resubmit") == "1" {
		d.Resubmit = true
		notice := &Flash{Kind: "info", Message: "Confirm your password, then submit the form again.", Inline: true}
		if d.Status == nil {
			d.Status = notice
		} else {
			d.Notice = notice
		}
	}
	v := a.NewView(w, r, "Confirm it's you")
	v.Data = d
	return v
}

func (a *Admin) handleSudoPage(w http.ResponseWriter, r *http.Request) {
	a.Render(w, r, http.StatusOK, "auth/sudo", a.sudoView(w, r, nil))
}

func (a *Admin) handleSudo(w http.ResponseWriter, r *http.Request) {
	pc, ok := a.deps.Auth.(passwordConfirmer)
	if !ok {
		a.Error(w, r, http.StatusServiceUnavailable, "Confirmation unavailable", "The server is not ready.")
		return
	}
	u, s := userFrom(r.Context()), sessionFrom(r.Context())
	if u == nil || s == nil {
		a.toLogin(w, r)
		return
	}
	// With a provider, reserve a code attempt before the slow checks so that
	// parallel requests cannot all pass a lock check and then all guess.
	var err error
	settled := false
	if a.sudo.secondFactor() != nil {
		if a.sudo.store.BeginCode(s.ID) {
			// Grant clears the count and a definite wrong code stays counted,
			// even if the request was cancelled meanwhile. Every other outcome,
			// a provider panic included, gives the attempt back.
			defer func() {
				if !settled {
					a.sudo.store.ReleaseCode(s.ID)
				}
			}()
		} else {
			err = errors.New("sudo: too many wrong codes")
		}
	}
	if err == nil {
		if s.UserID == nil {
			err = errors.New("sudo: session has no user")
		} else if err = pc.ConfirmPassword(r.Context(), *s.UserID, u.Email, r.PostFormValue("password")); err == nil {
			err = a.checkSecondFactor(r, s)
		}
	}
	settled = errors.Is(err, ErrWrongCode)
	if err != nil {
		if r.Context().Err() != nil {
			a.logger().Debug("sudo confirmation abandoned", "err", err)
			return
		}
		if errors.Is(err, errSudoProvider) {
			a.logger().Warn("sudo second factor could not decide", "err", err)
			v := a.sudoView(w, r, &Flash{Kind: "error", Message: sudoUnavailableMessage, Inline: true})
			a.Render(w, r, http.StatusServiceUnavailable, "auth/sudo", v)
			return
		}
		a.logger().Info("sudo confirmation failed", "err", err)
		v := a.sudoView(w, r, &Flash{Kind: "error", Message: sudoMessage, Inline: true})
		a.Render(w, r, http.StatusUnprocessableEntity, "auth/sudo", v)
		return
	}
	a.sudo.store.Grant(s.ID)
	settled = true
	http.Redirect(w, r, a.sudoNext(r), http.StatusSeeOther)
}

// checkSecondFactor asks for the code when the user has a second factor. It
// fails closed: a session without a user, or a provider that cannot say
// whether the user has a factor, is refused rather than let through.
func (a *Admin) checkSecondFactor(r *http.Request, s *session.Session) error {
	if s.UserID == nil {
		return errors.New("sudo: session has no user")
	}
	f := a.sudo.secondFactor()
	if f == nil {
		return nil
	}
	has, err := f.Has(r.Context(), *s.UserID)
	if err != nil {
		return fmt.Errorf("sudo: second factor lookup: %w", err)
	}
	if !has {
		return nil
	}
	if err := f.Verify(r.Context(), *s.UserID, r.PostFormValue("code")); err != nil {
		if errors.Is(err, ErrWrongCode) {
			return err
		}
		return fmt.Errorf("%w: %w", errSudoProvider, err)
	}
	return nil
}
