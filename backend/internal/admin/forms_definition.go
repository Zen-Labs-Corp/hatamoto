package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
)

// formDefinitionMaxBody bounds a definition save: 60 fields with options and rules.
const formDefinitionMaxBody = 1 << 20

// formDefinitionRequest is the whole definition the form designer saves.
// Version is the version the designer loaded (form.Form.Version).
type formDefinitionRequest struct {
	Version     string            `json:"version"`
	Name        string            `json:"name"`
	Description *string           `json:"description,omitempty"`
	Status      string            `json:"status"`
	Fields      []form.FormField  `json:"fields"`
	Settings    form.FormSettings `json:"settings"`
}

// formDefinitionReply answers a definition save. Errors are keyed by field
// id; problems with the form itself use "name" and "settings.<key>".
type formDefinitionReply struct {
	OK       bool                  `json:"ok"`
	Version  string                `json:"version,omitempty"`
	SavedAt  string                `json:"saved_at,omitempty"`
	Conflict bool                  `json:"conflict,omitzero"`
	Error    string                `json:"error,omitempty"`
	Errors   form.DefinitionErrors `json:"errors,omitempty"`
}

// handleFormDefinition saves a complete form definition as JSON. The version
// the designer holds must still be the stored one (409 otherwise); a bad
// definition is refused with a message per field (422).
func (a *Admin) handleFormDefinition(w http.ResponseWriter, r *http.Request) {
	fs, ok := a.formStore(w, r)
	if !ok {
		return
	}
	reply := func(status int, v formDefinitionReply) { a.writeJSON(w, status, v) }
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		reply(http.StatusNotFound, formDefinitionReply{Error: "This form does not exist."})
		return
	}
	var req formDefinitionRequest
	if status, _ := jsonx.DecodeRequest(w, r, &req, formDefinitionMaxBody); status == http.StatusRequestEntityTooLarge {
		reply(status, formDefinitionReply{Error: "This form is too large to save."})
		return
	} else if status != 0 {
		reply(status, formDefinitionReply{Error: "Invalid request body."})
		return
	}
	base, err := form.ParseVersion(req.Version)
	if err != nil {
		reply(http.StatusBadRequest, formDefinitionReply{Error: "Invalid form version."})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	var problems form.DefinitionErrors
	problem := func(key, msg string) { problems = append(problems, form.DefinitionError{FieldID: key, Message: msg}) }
	switch {
	case req.Name == "":
		problem("name", "Enter a name")
	case utf8.RuneCountInString(req.Name) > 120:
		problem("name", "Use at most 120 characters")
	}
	if n := req.Settings.NotifyEmail; n != nil && !validEmailList(*n) {
		problem("settings.notify_email", "Enter email addresses separated by commas")
	}
	if !safeRedirectTarget(req.Settings.SuccessRedirectURL) {
		problem("settings.success_redirect_url", "Enter a path like /thank-you or a full address starting with https://")
	}
	if len(problems) > 0 {
		reply(http.StatusUnprocessableEntity, formDefinitionReply{Error: "Some things need attention.", Errors: problems})
		return
	}

	saved, err := fs.SaveDefinition(r.Context(), id, base, &form.SaveDefinitionInput{
		Name: req.Name, Description: req.Description, Status: req.Status, Fields: req.Fields, Settings: req.Settings,
	})
	var defErrs form.DefinitionErrors
	switch {
	case err == nil:
	case errors.As(err, &defErrs):
		reply(http.StatusUnprocessableEntity, formDefinitionReply{Error: "Some fields need attention.", Errors: defErrs})
		return
	case errors.Is(err, form.ErrInvalidStatus):
		reply(http.StatusUnprocessableEntity, formDefinitionReply{Error: "Some things need attention.",
			Errors: form.DefinitionErrors{{FieldID: "status", Message: "Choose active, inactive or archived"}}})
		return
	case errors.Is(err, form.ErrFormNotFound):
		reply(http.StatusNotFound, formDefinitionReply{Error: "This form does not exist."})
		return
	case errors.Is(err, form.ErrVersionConflict):
		out := formDefinitionReply{Conflict: true, Error: "This form was changed by someone else. Reload it to see their changes."}
		if cur, gerr := fs.GetForm(r.Context(), id); gerr == nil {
			out.Version = cur.Version()
		}
		reply(http.StatusConflict, out)
		return
	default:
		a.logger().Error("save form definition failed", "err", err)
		reply(http.StatusInternalServerError, formDefinitionReply{Error: "The form could not be saved."})
		return
	}
	a.invalidate(r.Context())
	reply(http.StatusOK, formDefinitionReply{OK: true, Version: saved.Version(), SavedAt: saved.UpdatedAt.UTC().Format(time.RFC3339)})
}
