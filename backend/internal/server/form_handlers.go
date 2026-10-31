package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/core/internal/user"
)

// isValidEmail validates an email address using Go's net/mail parser.
func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// =============================================================================
// Form Builder Handlers
// =============================================================================

// handleListForms returns all forms for the tenant.
func (s *Server) handleListForms(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")

	forms, err := s.formService.ListForms(r.Context(), status)
	if err != nil {
		if errors.Is(err, form.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "invalid status filter")
			return
		}
		s.logger.Error("failed to list forms", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list forms")
		return
	}

	// Ensure empty array instead of null
	if forms == nil {
		forms = []*form.Form{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"forms": forms,
		"total": len(forms),
	})
}

// handleCreateForm creates a new form.
func (s *Server) handleCreateForm(w http.ResponseWriter, r *http.Request) {

	var input form.CreateFormInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	f, err := s.formService.CreateForm(r.Context(), &input)
	if err != nil {
		if errors.Is(err, form.ErrSlugAlreadyExists) {
			writeError(w, http.StatusConflict, "form slug already exists")
			return
		}
		if errors.Is(err, form.ErrInvalidFieldType) {
			writeError(w, http.StatusBadRequest, "invalid field type")
			return
		}
		s.logger.Error("failed to create form", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create form")
		return
	}

	s.invalidateRendered(r.Context()) // contact_form sections embed the fields
	writeJSON(w, http.StatusCreated, f)
}

// handleGetForm retrieves a form by ID.
func (s *Server) handleGetForm(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid form ID")
		return
	}

	f, err := s.formService.GetForm(r.Context(), id)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			writeError(w, http.StatusNotFound, "form not found")
			return
		}
		s.logger.Error("failed to get form", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get form")
		return
	}

	writeJSON(w, http.StatusOK, f)
}

// handleUpdateForm modifies a form.
func (s *Server) handleUpdateForm(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid form ID")
		return
	}

	var input form.UpdateFormInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	f, err := s.formService.UpdateForm(r.Context(), id, &input)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			writeError(w, http.StatusNotFound, "form not found")
			return
		}
		if errors.Is(err, form.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "invalid form status")
			return
		}
		if errors.Is(err, form.ErrInvalidFieldType) {
			writeError(w, http.StatusBadRequest, "invalid field type")
			return
		}
		s.logger.Error("failed to update form", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update form")
		return
	}

	s.invalidateRendered(r.Context())
	writeJSON(w, http.StatusOK, f)
}

// handleDeleteForm removes a form.
func (s *Server) handleDeleteForm(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid form ID")
		return
	}

	err = s.formService.DeleteForm(r.Context(), id)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			writeError(w, http.StatusNotFound, "form not found")
			return
		}
		s.logger.Error("failed to delete form", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete form")
		return
	}

	s.invalidateRendered(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"message": "form deleted"})
}

// handleListFormSubmissions returns submissions for a form.
func (s *Server) handleListFormSubmissions(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	formID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid form ID")
		return
	}

	params := &form.ListSubmissionsParams{
		Status: r.URL.Query().Get("status"),
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			params.Limit = limit
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil && offset >= 0 {
			params.Offset = offset
		}
	}

	submissions, total, err := s.formService.ListSubmissions(r.Context(), formID, params)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			writeError(w, http.StatusNotFound, "form not found")
			return
		}
		if errors.Is(err, form.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "invalid status filter")
			return
		}
		s.logger.Error("failed to list submissions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list submissions")
		return
	}

	// Ensure empty array instead of null
	if submissions == nil {
		submissions = []*form.FormSubmission{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"submissions": submissions,
		"total":       total,
		"limit":       params.Limit,
		"offset":      params.Offset,
	})
}

// handleGetFormSubmission retrieves a single submission.
func (s *Server) handleGetFormSubmission(w http.ResponseWriter, r *http.Request) {
	submissionIDStr := r.PathValue("submissionId")
	submissionID, err := uuid.Parse(submissionIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid submission ID")
		return
	}

	submission, err := s.formService.GetSubmission(r.Context(), submissionID)
	if err != nil {
		if errors.Is(err, form.ErrSubmissionNotFound) {
			writeError(w, http.StatusNotFound, "submission not found")
			return
		}
		s.logger.Error("failed to get submission", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get submission")
		return
	}

	writeJSON(w, http.StatusOK, submission)
}

// handleUpdateFormSubmission modifies a submission status.
func (s *Server) handleUpdateFormSubmission(w http.ResponseWriter, r *http.Request) {
	submissionIDStr := r.PathValue("submissionId")
	submissionID, err := uuid.Parse(submissionIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid submission ID")
		return
	}

	var input form.UpdateSubmissionInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	submission, err := s.formService.UpdateSubmission(r.Context(), submissionID, &input)
	if err != nil {
		if errors.Is(err, form.ErrSubmissionNotFound) {
			writeError(w, http.StatusNotFound, "submission not found")
			return
		}
		if errors.Is(err, form.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "invalid submission status")
			return
		}
		s.logger.Error("failed to update submission", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update submission")
		return
	}

	writeJSON(w, http.StatusOK, submission)
}

// handleDeleteFormSubmission removes a submission.
func (s *Server) handleDeleteFormSubmission(w http.ResponseWriter, r *http.Request) {
	submissionIDStr := r.PathValue("submissionId")
	submissionID, err := uuid.Parse(submissionIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid submission ID")
		return
	}

	err = s.formService.DeleteSubmission(r.Context(), submissionID)
	if err != nil {
		if errors.Is(err, form.ErrSubmissionNotFound) {
			writeError(w, http.StatusNotFound, "submission not found")
			return
		}
		s.logger.Error("failed to delete submission", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete submission")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "submission deleted"})
}

// handleEmailFormSubmission forwards a submission via email to an arbitrary recipient.
// POST /api/v1/forms/{id}/submissions/{submissionId}/email
func (s *Server) handleEmailFormSubmission(w http.ResponseWriter, r *http.Request) {
	if s.emailService == nil {
		writeError(w, http.StatusServiceUnavailable, "email service not configured")
		return
	}

	formIDStr := r.PathValue("id")
	formID, err := uuid.Parse(formIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid form ID")
		return
	}

	submissionIDStr := r.PathValue("submissionId")
	submissionID, err := uuid.Parse(submissionIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid submission ID")
		return
	}

	var input struct {
		To string `json:"to"`
	}
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	input.To = strings.TrimSpace(input.To)
	if input.To == "" {
		writeError(w, http.StatusBadRequest, "recipient email is required")
		return
	}

	// Parse and validate all recipient emails
	recipients := strings.Split(input.To, ",")
	var validRecipients []string
	for _, r := range recipients {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !isValidEmail(r) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid email address: %s", r))
			return
		}
		validRecipients = append(validRecipients, r)
	}
	if len(validRecipients) == 0 {
		writeError(w, http.StatusBadRequest, "at least one valid recipient email is required")
		return
	}

	// Get form and submission
	formObj, err := s.formService.GetForm(r.Context(), formID)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			writeError(w, http.StatusNotFound, "form not found")
			return
		}
		s.logger.Error("failed to get form", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get form")
		return
	}

	submission, err := s.formService.GetSubmission(r.Context(), submissionID)
	if err != nil {
		if errors.Is(err, form.ErrSubmissionNotFound) {
			writeError(w, http.StatusNotFound, "submission not found")
			return
		}
		s.logger.Error("failed to get submission", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get submission")
		return
	}

	// Build field data for email template
	fields := make([]email.FormFieldData, 0, len(formObj.Fields))
	for _, field := range formObj.Fields {
		if val, ok := submission.Data[field.Name]; ok {
			fields = append(fields, email.FormFieldData{
				Label: field.Label,
				Value: formatFieldValue(val),
			})
		}
	}

	emailData := email.FormSubmissionData{
		FormName:      formObj.Name,
		FormSlug:      formObj.Slug,
		SubmissionID:  submission.ID.String(),
		SubmittedAt:   submission.CreatedAt,
		Fields:        fields,
		SubmitterIP:   submission.Metadata.IPAddress,
		SubmitterPage: s.redactPageURL(submission.Metadata.PageURL),
		SiteName:      s.siteName(context.Background()),
	}

	if !s.emailService.IsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "email is not configured")
		return
	}

	// Get the current user's email for the "forwarded by" attribution
	claims, _ := middleware.GetClaims(r.Context())
	forwardedBy := ""
	if claims != nil {
		forwardedBy = claims.Email
	}

	if err := s.emailService.SendFormSubmissionForward(r.Context(), validRecipients, emailData, forwardedBy); err != nil {
		s.logger.Error("failed to forward submission email",
			slog.String("error", err.Error()),
			slog.String("form_id", formObj.ID.String()),
			slog.String("submission_id", submission.ID.String()),
		)
		writeError(w, http.StatusInternalServerError, "failed to send email")
		return
	}

	s.logger.Info("form submission forwarded via email",
		slog.String("form_id", formObj.ID.String()),
		slog.String("submission_id", submission.ID.String()),
		slog.Any("recipients", validRecipients),
	)

	writeJSON(w, http.StatusOK, map[string]string{"message": "email sent successfully"})
}

// handleGetPublicForm returns a form by slug for public rendering (no auth required).
func (s *Server) handleGetPublicForm(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "form slug is required")
		return
	}

	// Get form by slug
	formObj, err := s.formService.GetFormBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			writeError(w, http.StatusNotFound, "form not found")
			return
		}
		s.logger.Error("failed to get form", "error", err, "slug", slug)
		writeError(w, http.StatusInternalServerError, "failed to get form")
		return
	}

	// Only return active forms
	if formObj.Status != form.StatusActive {
		writeError(w, http.StatusNotFound, "form not found")
		return
	}

	// Return a sanitized response (without tenant_id)
	response := map[string]interface{}{
		"id":          formObj.ID.String(),
		"name":        formObj.Name,
		"slug":        formObj.Slug,
		"description": formObj.Description,
		"fields":      formObj.Fields,
		"settings":    publicFormSettings(formObj.Settings),
	}

	writeJSON(w, http.StatusOK, response)
}

// publicFormSettings returns only the settings a public renderer needs.
// Internal routing (notify_email) and server-side behaviour flags
// (confirmation email body, storage) are never exposed to anonymous clients.
func publicFormSettings(s form.FormSettings) map[string]interface{} {
	return map[string]interface{}{
		"submit_button_text":   s.SubmitButtonText,
		"success_message":      s.SuccessMessage,
		"success_redirect_url": s.SuccessRedirectURL,
		"enable_captcha":       s.EnableCaptcha,
	}
}

// submitMeta is the request-derived metadata stored with a submission.
type submitMeta struct {
	IPAddress string
	// TrustedIP is the trusted-proxy-aware client IP, sent to third parties
	// (the captcha provider). It equals IPAddress: both come from the shared
	// middleware.GetClientIP.
	TrustedIP string
	UserAgent string
	Referrer  string
	PageURL   string
	// Host is the request host, used to tell own-site referrers from external ones.
	Host string
}

// submitResult is the outcome of a stored submission.
type submitResult struct {
	SubmissionID string
	Message      string
	RedirectURL  string
}

// submitError is a submission failure carrying the HTTP status and message
// the JSON endpoint reports.
type submitError struct {
	status int
	msg    string
	fields map[string]string // per-field messages of a validation failure
	code   string            // stable machine-readable reason, for clients to branch on
}

func (e *submitError) Error() string { return e.msg }

func writeSubmitError(w http.ResponseWriter, err error) {
	var se *submitError
	if errors.As(err, &se) {
		if len(se.fields) > 0 {
			writeJSON(w, se.status, map[string]any{"error": se.msg, "fields": se.fields})
			return
		}
		if se.code != "" {
			writeJSON(w, se.status, map[string]string{"error": se.msg, "code": se.code})
			return
		}
		writeError(w, se.status, se.msg)
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to process submission")
}

// activeFormBySlug loads the form a public submission targets; inactive forms
// look like missing ones.
func (s *Server) activeFormBySlug(ctx context.Context, slug string) (*form.Form, error) {
	formObj, err := s.formService.GetFormBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, form.ErrFormNotFound) {
			return nil, &submitError{status: http.StatusNotFound, msg: "form not found"}
		}
		s.logger.Error("failed to get form", "error", err, "slug", slug)
		return nil, &submitError{status: http.StatusInternalServerError, msg: "failed to process submission"}
	}
	if formObj.Status != form.StatusActive {
		return nil, &submitError{status: http.StatusNotFound, msg: "form not found"}
	}
	return formObj, nil
}

// submitPublicForm stores a public submission for the form identified by slug
// and runs the follow-up notifications. It is shared by the JSON and the
// urlencoded endpoints.
func (s *Server) submitPublicForm(ctx context.Context, slug string, data map[string]interface{}, ctl spamControl, meta submitMeta) (*submitResult, error) {
	formObj, err := s.activeFormBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	return s.storeFormSubmission(ctx, formObj, slug, data, ctl, meta)
}

func (s *Server) storeFormSubmission(ctx context.Context, formObj *form.Form, slug string, data map[string]interface{}, ctl spamControl, meta submitMeta) (*submitResult, error) {
	// One validator for every public submit path: unknown fields are dropped,
	// required, type, choices, length and pattern checks are enforced.
	data, fieldErrs := form.ValidateSubmission(formObj.Fields, data)
	if len(fieldErrs) > 0 {
		return nil, &submitError{status: http.StatusBadRequest, msg: "validation failed", fields: fieldErrs}
	}
	status, serr := s.checkSpam(ctx, formObj, data, ctl, meta.TrustedIP)
	if serr != nil {
		return nil, serr
	}
	input := &form.CreateSubmissionInput{
		Status:      status,
		Data:        data,
		Attribution: attributionFromControl(ctl.Attribution, meta.Host),
		Metadata: form.SubmissionMetadata{
			IPAddress: meta.IPAddress,
			UserAgent: meta.UserAgent,
			Referrer:  meta.Referrer,
			PageURL:   meta.PageURL,
		},
	}

	// Create submission
	submission, err := s.formService.CreateSubmission(ctx, formObj.ID, input)
	if err != nil {
		s.logger.Error("failed to create submission", "error", err, "form_id", formObj.ID.String())
		return nil, &submitError{status: http.StatusInternalServerError, msg: "failed to process submission"}
	}

	// A submission classed as spam is kept for review but reaches nobody: no
	// notification, no confirmation, no hook or webhook.
	if status != form.SubmissionStatusSpam {
		s.sendFormSubmissionEmail(formObj, submission, data)
		s.sendFormConfirmationEmail(formObj, submission, data)
		s.fireFormSubmitted(ctx, formObj, submission, data, meta)
	}

	return &submitResult{
		SubmissionID: submission.ID.String(),
		Message:      formObj.Settings.SuccessMessage,
		RedirectURL:  formObj.Settings.SuccessRedirectURL,
	}, nil
}

// Control fields carry the spam checks' evidence, not submission data. The
// handlers read them and strip them before validation, rules and storage.
const (
	timingField  = "hm_ts"      // timing token fetched by hm.js at page load
	captchaField = "hm_captcha" // captcha response normalised by hm.js

	// attributionField carries the raw first/last touch inputs added by
	// hm-track.js at submit time. It is evidence for the attribution columns,
	// never submission data.
	attributionField = "hm_attr"
)

// providerCaptchaFields are the names the providers' own widgets post under,
// used by native (non-enhanced) form posts.
var providerCaptchaFields = []string{"cf-turnstile-response", "g-recaptcha-response", "h-captcha-response"}

// spamControl is the evidence a submission brings for the spam pipeline.
type spamControl struct {
	Timing      string
	Captcha     string
	Attribution string // raw hm_attr; may be empty or garbled, never rejects
}

// attributionFromControl classifies a raw hm_attr value into the stored
// attribution. Missing or unparseable input yields nil.
func attributionFromControl(raw, host string) *form.Attribution {
	a, ok := analytics.ParseAttribution(raw, agents.Default(), host)
	if !ok {
		return nil
	}
	conv := func(t *analytics.Touch) *form.Touch {
		if t == nil {
			return nil
		}
		return &form.Touch{Channel: t.Channel, Source: t.Source, Medium: t.Medium, Campaign: t.Campaign, Landing: t.Landing}
	}
	return &form.Attribution{First: conv(a.First), Last: conv(a.Last)}
}

func isControlField(k string) bool {
	if k == timingField || k == captchaField || k == attributionField {
		return true
	}
	for _, n := range providerCaptchaFields {
		if k == n {
			return true
		}
	}
	return false
}

// takeControl removes the control fields from a JSON body and returns them.
func takeControl(data map[string]interface{}) spamControl {
	str := func(k string) string { v, _ := data[k].(string); return v }
	ctl := spamControl{Timing: str(timingField), Captcha: str(captchaField), Attribution: str(attributionField)}
	for _, n := range providerCaptchaFields {
		if ctl.Captcha == "" {
			ctl.Captcha = str(n)
		}
	}
	for k := range data {
		if isControlField(k) {
			delete(data, k)
		}
	}
	return ctl
}

// controlFromValues reads the control fields of a urlencoded post.
func controlFromValues(post url.Values) spamControl {
	ctl := spamControl{Timing: post.Get(timingField), Captcha: post.Get(captchaField), Attribution: post.Get(attributionField)}
	for _, n := range providerCaptchaFields {
		if ctl.Captcha == "" {
			ctl.Captcha = post.Get(n)
		}
	}
	return ctl
}

// checkSpam runs the spam pipeline on validated submission data. It returns the
// status to store the submission with, or the error to answer with: a failed
// captcha and a refused rule trip are both 422.
func (s *Server) checkSpam(ctx context.Context, f *form.Form, data map[string]interface{}, ctl spamControl, ip string) (string, *submitError) {
	if s.spam == nil {
		return form.SubmissionStatusNew, nil
	}
	verdict, reason := s.spam.Check(ctx, spam.Input{
		Slug: f.Slug, CaptchaRequired: f.Settings.EnableCaptcha, Data: data,
		Timing: ctl.Timing, Captcha: ctl.Captcha, IP: ip,
	})
	switch verdict {
	case spam.CaptchaFail:
		s.logger.Info("form captcha failed", "form_id", f.ID.String(), "reason", reason)
		return "", &submitError{status: http.StatusUnprocessableEntity, msg: "Please complete the spam check and try again.", code: "captcha_required"}
	case spam.Refuse:
		s.logger.Info("form submission refused", "form_id", f.ID.String(), "reason", reason)
		return "", &submitError{status: http.StatusUnprocessableEntity, msg: "Your message could not be accepted.", code: "refused"}
	case spam.Spam:
		s.logger.Info("form submission saved as spam", "form_id", f.ID.String(), "reason", reason)
		return form.SubmissionStatusSpam, nil
	}
	return form.SubmissionStatusNew, nil
}

// handleFormToken issues the timing token hm.js writes into hm_ts. It is
// fetched at page load, never rendered, so cached pages stay visitor-neutral.
func (s *Server) handleFormToken(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if _, err := s.activeFormBySlug(r.Context(), slug); err != nil {
		writeSubmitError(w, err)
		return
	}
	if s.spam == nil {
		writeError(w, http.StatusNotFound, "not available")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": s.spam.IssueToken(slug)})
}

// honeypotField is the hidden form field humans leave empty.
const honeypotField = "hm_website"

// honeypotFilled reports whether a JSON value for the honeypot field counts as
// filled: any non-empty string, and any non-string value but null.
func honeypotFilled(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	}
	return true
}

// handlePublicFormSubmit handles public form submissions (no auth required).
func (s *Server) handlePublicFormSubmit(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "form slug is required")
		return
	}

	formObj, err := s.activeFormBySlug(r.Context(), slug)
	if err != nil {
		writeSubmitError(w, err)
		return
	}

	// Parse submission data: JSON, or a urlencoded HTML form post.
	var data map[string]interface{}
	var ctl spamControl
	var trapped bool
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "application/x-www-form-urlencoded" {
		data, err = parseFormValues(w, r)
		trapped = err == nil && r.PostForm.Get(honeypotField) != ""
		if err == nil {
			ctl = controlFromValues(r.PostForm)
		}
	} else {
		err = parseJSON(r, &data)
		trapped = err == nil && honeypotFilled(data[honeypotField])
		ctl = takeControl(data)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// A sandboxed page has no session; its proof of a real page load is the
	// timing token.
	if originNull(r) && !s.timingTokenOK(slug, ctl.Timing) {
		writeError(w, http.StatusForbidden, "form token required")
		return
	}

	// Honeypot, as on the no-JS path: bots fill the hidden field. Answer as if
	// it worked (same shape, a submission id that matches nothing), store and
	// notify nothing.
	if trapped {
		response := map[string]interface{}{
			"success":       true,
			"message":       formObj.Settings.SuccessMessage,
			"submission_id": uuid.NewString(),
		}
		if formObj.Settings.SuccessRedirectURL != "" {
			response["redirect_url"] = formObj.Settings.SuccessRedirectURL
		}
		writeJSON(w, http.StatusCreated, response)
		return
	}

	res, err := s.storeFormSubmission(r.Context(), formObj, slug, data, ctl, submitMeta{
		IPAddress: middleware.GetClientIP(r),
		TrustedIP: middleware.GetClientIP(r),
		UserAgent: r.UserAgent(),
		Referrer:  r.Referer(),
		PageURL:   r.Header.Get("X-Page-URL"),
		Host:      r.Host,
	})
	if err != nil {
		writeSubmitError(w, err)
		return
	}

	// Return success response with form settings
	response := map[string]interface{}{
		"success":       true,
		"message":       res.Message,
		"submission_id": res.SubmissionID,
	}

	if res.RedirectURL != "" {
		response["redirect_url"] = res.RedirectURL
	}

	writeJSON(w, http.StatusCreated, response)
}

// sendFormSubmissionEmail sends an email notification for a form submission.
// This is a non-blocking operation - errors are logged but don't affect the response.
func (s *Server) sendFormSubmissionEmail(formObj *form.Form, submission *form.FormSubmission, data map[string]interface{}) {
	// Check if email service is available and notification email is configured
	if s.emailService == nil {
		return
	}

	notifyEmail := formObj.Settings.NotifyEmail
	if notifyEmail == nil || *notifyEmail == "" {
		s.logger.Debug("no notification email configured for form", slog.String("form_id", formObj.ID.String()))
		return
	}
	if !s.emailService.IsConfigured() {
		s.logger.Warn("form notification not sent: SMTP is not configured",
			slog.String("form_id", formObj.ID.String()),
			slog.String("submission_id", submission.ID.String()))
		return
	}

	// Build field data for email template
	fields := make([]email.FormFieldData, 0, len(formObj.Fields))
	for _, field := range formObj.Fields {
		if val, ok := data[field.Name]; ok {
			fields = append(fields, email.FormFieldData{
				Label: field.Label,
				Value: formatFieldValue(val),
			})
		}
	}

	// Build email data
	emailData := email.FormSubmissionData{
		FormName:      formObj.Name,
		FormSlug:      formObj.Slug,
		SubmissionID:  submission.ID.String(),
		SubmittedAt:   time.Now(),
		Fields:        fields,
		SubmitterIP:   submission.Metadata.IPAddress,
		SubmitterPage: s.redactPageURL(submission.Metadata.PageURL),
		SiteName:      s.siteName(context.Background()),
	}

	// Send email asynchronously to not block the response.
	// Use a detached context because r.Context() will be cancelled when the
	// HTTP handler returns, which would cause the email send to fail.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var admins, others []string
		for _, rcpt := range strings.Split(*notifyEmail, ",") {
			rcpt = strings.TrimSpace(rcpt)
			switch {
			case rcpt == "":
			case s.isAdminAccountEmail(ctx, rcpt):
				admins = append(admins, rcpt)
			default:
				others = append(others, rcpt)
			}
		}

		// Only a recipient with an admin account gets the link to the
		// submission; it names the private admin address.
		adminData := emailData
		adminData.AdminURL = s.adminPathValue() + "/settings/submissions/" + submission.ID.String()
		for _, send := range []struct {
			to   []string
			data email.FormSubmissionData
		}{{admins, adminData}, {others, emailData}} {
			if err := s.emailService.SendFormSubmission(ctx, send.to, send.data); err != nil {
				s.logger.Error("failed to send form submission email",
					slog.String("error", err.Error()),
					slog.String("form_id", formObj.ID.String()),
					slog.String("submission_id", submission.ID.String()),
				)
			}
		}
	}()
}

// isAdminAccountEmail reports whether addr belongs to an account that can sign
// in to the admin (owner or editor). A failed lookup counts as no, so the
// admin address never goes to someone who might not hold an account.
func (s *Server) isAdminAccountEmail(ctx context.Context, addr string) bool {
	if s.userRepo == nil {
		return false
	}
	ctx = database.WithSuperadmin(ctx)
	// The lookup is an exact match, so try the address as typed, then lowercased.
	cands := []string{addr}
	if lower := strings.ToLower(addr); lower != addr {
		cands = append(cands, lower)
	}
	for _, cand := range cands {
		u, err := s.userRepo.GetByEmail(ctx, cand)
		if errors.Is(err, user.ErrUserNotFound) {
			continue
		}
		if err != nil {
			s.logger.Warn("form notification: admin account lookup failed, sending without the admin link",
				slog.String("error", err.Error()))
			return false
		}
		return u.Role == user.RoleOwner || u.Role == user.RoleEditor
	}
	return false
}

// formatFieldValue converts a field value to a display string.
func formatFieldValue(val interface{}) string {
	switch v := val.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(parts, ", ")
	case bool:
		if v {
			return "Yes"
		}
		return "No"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// formConfirmationSender is the slice of the email service the form handlers
// use to confirm a submission to its submitter (faked in tests).
type formConfirmationSender interface {
	SendFormConfirmation(ctx context.Context, to string, data email.FormConfirmationData) error
}

const defaultFormConfirmationMessage = "Thank you. We have received your submission."

// formConfirmation builds the confirmation email for a submission, or reports
// ok=false when none should be sent: confirmation disabled, or no `email`-type
// field holding a valid bare address. Only the submitter's name (a field
// called "name") is echoed; no other submitted value is used.
func formConfirmation(formObj *form.Form, data map[string]interface{}, siteName string) (to string, msg email.FormConfirmationData, ok bool) {
	if !formObj.Settings.ConfirmationEmail {
		return "", msg, false
	}
	for _, f := range formObj.Fields {
		if f.Type != form.FieldTypeEmail {
			continue
		}
		raw, _ := data[f.Name].(string)
		raw = strings.TrimSpace(raw)
		addr, err := mail.ParseAddress(raw)
		if err != nil || addr.Address != raw || strings.ContainsAny(raw, "\r\n") {
			continue
		}
		to = raw
		break
	}
	if to == "" {
		return "", msg, false
	}

	// The name is echoed only when the form itself defines a `name` field.
	name := ""
	for _, f := range formObj.Fields {
		if f.Name == "name" {
			name, _ = data["name"].(string)
			break
		}
	}
	name = strings.TrimSpace(name)
	if strings.ContainsAny(name, "\r\n") {
		name = ""
	}
	if r := []rune(name); len(r) > 100 {
		name = string(r[:100])
	}

	message := strings.TrimSpace(formObj.Settings.ConfirmationMessage)
	if message == "" {
		message = strings.TrimSpace(formObj.Settings.SuccessMessage)
	}
	if message == "" {
		message = defaultFormConfirmationMessage
	}
	return to, email.FormConfirmationData{
		FormName: formObj.Name,
		Name:     name,
		Message:  message,
		SiteName: siteName,
	}, true
}

// sendFormConfirmationEmail sends the submitter a receipt when the form has
// settings.confirmation_email enabled. At most one email per submission, one
// per recipient per form per 10 minutes (per process), and at most 4 sends in
// flight (excess is dropped and logged without PII). Sent asynchronously on a
// detached context, errors are logged only.
func (s *Server) sendFormConfirmationEmail(formObj *form.Form, submission *form.FormSubmission, data map[string]interface{}) {
	if s.formConfirmationSender == nil {
		return
	}
	to, msg, ok := formConfirmation(formObj, data, s.siteName(context.Background()))
	if !ok {
		return
	}
	if !s.confirmThrottle.allow(formObj.ID.String(), to, time.Now()) {
		return // submission is stored; the receipt is silently skipped
	}
	if !s.confirmThrottle.tryAcquire() {
		s.logger.Warn("form confirmation email dropped: too many in flight",
			slog.String("form_id", formObj.ID.String()),
			slog.String("submission_id", submission.ID.String()),
		)
		return
	}
	go func() {
		defer s.confirmThrottle.release()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.formConfirmationSender.SendFormConfirmation(ctx, to, msg); err != nil {
			s.logger.Error("failed to send form confirmation email",
				slog.String("error", err.Error()),
				slog.String("form_id", formObj.ID.String()),
				slog.String("submission_id", submission.ID.String()),
			)
		}
	}()
}

// redactPageURL hides the live admin address in a client-sent page URL, so a
// form submitted from inside the admin does not mail the address out.
func (s *Server) redactPageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	red := s.redactAdminPath(u.Path)
	if red == u.Path {
		return raw
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = red, "", "", ""
	return u.String()
}
