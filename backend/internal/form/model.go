// Package form provides domain models and repository interfaces for form management.
package form

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Form status constants.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusArchived = "archived"
)

// Submission status constants.
const (
	SubmissionStatusNew      = "new"
	SubmissionStatusRead     = "read"
	SubmissionStatusArchived = "archived"
	SubmissionStatusSpam     = "spam"
)

// Field type constants.
const (
	FieldTypeText     = "text"
	FieldTypeEmail    = "email"
	FieldTypePhone    = "phone"
	FieldTypeTextarea = "textarea"
	FieldTypeSelect   = "select"
	FieldTypeRadio    = "radio"
	FieldTypeCheckbox = "checkbox"
	FieldTypeDate     = "date"
	FieldTypeNumber   = "number"
	FieldTypeURL      = "url"

	FieldTypeHeading   = "heading"
	FieldTypeParagraph = "paragraph"
	FieldTypeDivider   = "divider"
	FieldTypeHidden    = "hidden"
	FieldTypeConsent   = "consent"
)

// Common errors.
var (
	ErrFormNotFound       = errors.New("form not found")
	ErrSubmissionNotFound = errors.New("submission not found")
	ErrSlugAlreadyExists  = errors.New("form slug already exists")
	ErrInvalidStatus      = errors.New("invalid form status")
	ErrInvalidFieldType   = errors.New("invalid field type")
	// ErrVersionConflict means the form was saved by someone else after the
	// version the caller based its edit on.
	ErrVersionConflict = errors.New("form was changed by someone else")
)

// Form represents a form definition.
type Form struct {
	ID          uuid.UUID    `json:"id"`
	Name        string       `json:"name"`
	Slug        string       `json:"slug"`
	Description *string      `json:"description,omitempty"`
	Fields      []FormField  `json:"fields"`
	Settings    FormSettings `json:"settings"`
	Status      string       `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Version identifies the saved state of the form for conflict detection: its
// last update time at the microsecond precision Postgres stores.
func (f *Form) Version() string { return f.UpdatedAt.UTC().Format(time.RFC3339Nano) }

// ParseVersion reads a string produced by Version.
func ParseVersion(v string) (time.Time, error) { return time.Parse(time.RFC3339Nano, v) }

// FormField represents a single field in a form.
type FormField struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Label        string            `json:"label"`
	Name         string            `json:"name"`
	Placeholder  string            `json:"placeholder,omitempty"`
	Required     bool              `json:"required"`
	Options      []FieldOption     `json:"options,omitempty"`
	Validation   *FieldValidation  `json:"validation,omitempty"`
	DefaultValue string            `json:"default_value,omitempty"`
	HelpText     string            `json:"help_text,omitempty"`
	Width        string            `json:"width,omitempty"` // "full", "half", "third"
	Position     int               `json:"position"`
	Attributes   map[string]string `json:"attributes,omitempty"`

	// Logic shows the field only while its condition holds.
	Logic *FieldLogic `json:"logic,omitempty"`
	// InEmail is nil when the field is included in the notification email.
	InEmail        *bool       `json:"in_email,omitempty"`
	NameLocked     bool        `json:"name_locked,omitzero"`
	BlockFreeEmail bool        `json:"block_free_email,omitzero"` // email type only
	Text           string      `json:"text,omitempty"`            // heading/paragraph content
	Link           string      `json:"link,omitempty"`            // consent link: /path or http(s) URL
	Hidden         *HiddenSpec `json:"hidden,omitempty"`
}

// FieldLogic is a show-when condition on another field, referenced by id.
// Ops: is, is_not, contains, filled, empty.
type FieldLogic struct {
	FieldID string `json:"field_id"`
	Op      string `json:"op"`
	Value   string `json:"value,omitempty"`
}

// HiddenSpec says how a hidden field's value is resolved on the server.
// Source is one of param, page, referrer, static.
type HiddenSpec struct {
	Source string `json:"source"`
	Param  string `json:"param,omitempty"`
	Value  string `json:"value,omitempty"`
}

// IsLayout reports whether the field is display-only and carries no answer.
func (f FormField) IsLayout() bool {
	switch f.Type {
	case FieldTypeHeading, FieldTypeParagraph, FieldTypeDivider:
		return true
	default:
		return false
	}
}

// IsInput reports whether the field can hold a submitted value.
func (f FormField) IsInput() bool { return !f.IsLayout() }

// InEmailOn reports whether the field belongs in the notification email.
func (f FormField) InEmailOn() bool { return f.InEmail == nil || *f.InEmail }

// FieldOption represents an option for select, radio, or checkbox fields.
type FieldOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// FieldValidation represents validation rules for a field.
type FieldValidation struct {
	MinLength *int    `json:"min_length,omitempty"`
	MaxLength *int    `json:"max_length,omitempty"`
	Min       *int    `json:"min,omitempty"`
	Max       *int    `json:"max,omitempty"`
	Pattern   *string `json:"pattern,omitempty"`
	Message   *string `json:"message,omitempty"`
}

// FormSettings represents form-level settings.
type FormSettings struct {
	SubmitButtonText   string  `json:"submit_button_text,omitempty"`
	SuccessMessage     string  `json:"success_message,omitempty"`
	SuccessRedirectURL string  `json:"success_redirect_url,omitempty"`
	EnableCaptcha      bool    `json:"enable_captcha"`
	NotifyEmail        *string `json:"notify_email,omitempty"`
	ConfirmationEmail  bool    `json:"confirmation_email"`
	// ConfirmationMessage is the body of the submitter's confirmation email;
	// SuccessMessage is used when it is empty.
	ConfirmationMessage string `json:"confirmation_message,omitempty"`
	// StoreSubmissions is the legacy keep switch. Nothing enforces it: it was
	// never read before the Settings tab and API-created forms default to false.
	StoreSubmissions bool `json:"store_submissions"`
	// StoreOff keeps the answers out of the database (they are only mailed
	// and sent to hooks). Inverted so an absent key keeps them.
	StoreOff bool `json:"store_off,omitzero"`

	// NotifySubject is the notification subject; {{field_name}} tokens are
	// filled from the submission.
	NotifySubject string `json:"notify_subject,omitempty"`
	// NotifyReplyTo names an email field whose value becomes the Reply-To.
	NotifyReplyTo       string `json:"notify_reply_to,omitempty"`
	ConfirmationSubject string `json:"confirmation_subject,omitempty"`
	// HoneypotOff is inverted so an absent key keeps the trap field on.
	HoneypotOff bool `json:"honeypot_off,omitzero"`
	// RateLimitPerHour caps sends per visitor per hour; 0 is off.
	RateLimitPerHour int `json:"rate_limit_per_hour,omitzero"`
	// BlockedWords marks a submission containing any of them as spam.
	BlockedWords []string `json:"blocked_words,omitempty"`
	// RetentionDays deletes submissions older than this; 0 keeps them.
	RetentionDays int `json:"retention_days,omitzero"`
	// NoIP stops the submitter's address being stored.
	NoIP bool `json:"no_ip,omitzero"`
}

// FormSubmission represents a submitted form entry.
type FormSubmission struct {
	ID        uuid.UUID          `json:"id"`
	FormID    uuid.UUID          `json:"form_id"`
	Data      map[string]any     `json:"data"`
	Metadata  SubmissionMetadata `json:"metadata"`
	Status    string             `json:"status"`
	CreatedAt time.Time          `json:"created_at"`
	// Attribution is where the submitter came from; nil when none was recorded.
	Attribution *Attribution `json:"attribution,omitempty"`

	// Populated on read
	FormName *string `json:"form_name,omitempty"`
}

// Touch is one classified visit source: channel, source, medium, campaign and
// landing path. It never holds a URL, address or identifier.
type Touch struct {
	Channel  string `json:"channel,omitempty"`
	Source   string `json:"source,omitempty"`
	Medium   string `json:"medium,omitempty"`
	Campaign string `json:"campaign,omitempty"`
	Landing  string `json:"landing,omitempty"`
}

// Attribution is the first and last touch of the tab that submitted a form.
type Attribution struct {
	First *Touch `json:"first,omitempty"`
	Last  *Touch `json:"last,omitempty"`
}

// SubmissionMetadata contains metadata about the submission.
type SubmissionMetadata struct {
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Referrer  string `json:"referrer,omitempty"`
	PageURL   string `json:"page_url,omitempty"`
}

// CreateFormInput represents input for creating a new form.
type CreateFormInput struct {
	Name        string       `json:"name"`
	Slug        string       `json:"slug,omitempty"`
	Description *string      `json:"description,omitempty"`
	Fields      []FormField  `json:"fields"`
	Settings    FormSettings `json:"settings"`
}

// UpdateFormInput represents input for updating a form.
type UpdateFormInput struct {
	Name        *string       `json:"name,omitempty"`
	Description *string       `json:"description,omitempty"`
	Fields      []FormField   `json:"fields,omitempty"`
	Settings    *FormSettings `json:"settings,omitempty"`
	Status      *string       `json:"status,omitempty"`
}

// SaveDefinitionInput is a complete form definition. Unlike UpdateFormInput it
// replaces fields and settings wholesale.
type SaveDefinitionInput struct {
	Name        string       `json:"name"`
	Description *string      `json:"description,omitempty"`
	Status      string       `json:"status"`
	Fields      []FormField  `json:"fields"`
	Settings    FormSettings `json:"settings"`
}

// CreateSubmissionInput represents input for creating a form submission.
type CreateSubmissionInput struct {
	Data     map[string]any     `json:"data"`
	Metadata SubmissionMetadata `json:"metadata"`
	// Status is the initial status; empty means SubmissionStatusNew. Set by
	// the server (spam pipeline), never read from a request body.
	Status string `json:"-"`
	// Attribution is set by the server from the classified hm_attr field.
	Attribution *Attribution `json:"-"`
}

// UpdateSubmissionInput represents input for updating a submission.
type UpdateSubmissionInput struct {
	Status *string `json:"status,omitempty"`
}

// ListSubmissionsParams represents parameters for listing submissions.
type ListSubmissionsParams struct {
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitzero"`
	Offset int    `json:"offset,omitzero"`
	// After continues a listing after this submission (keyset paging, newest
	// first): rows are neither repeated nor skipped when new ones arrive. It
	// replaces Offset and skips the total count.
	After *SubmissionCursor `json:"-"`
}

// SubmissionCursor is the position of the last submission of a page.
type SubmissionCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ValidStatus checks if a form status is valid.
func ValidStatus(status string) bool {
	switch status {
	case StatusActive, StatusInactive, StatusArchived:
		return true
	default:
		return false
	}
}

// ValidSubmissionStatus checks if a submission status is valid.
func ValidSubmissionStatus(status string) bool {
	switch status {
	case SubmissionStatusNew, SubmissionStatusRead, SubmissionStatusArchived, SubmissionStatusSpam:
		return true
	default:
		return false
	}
}

// ValidFieldType checks if a field type is valid.
func ValidFieldType(fieldType string) bool {
	switch fieldType {
	case FieldTypeText, FieldTypeEmail, FieldTypePhone, FieldTypeTextarea,
		FieldTypeSelect, FieldTypeRadio, FieldTypeCheckbox, FieldTypeDate,
		FieldTypeNumber, FieldTypeURL, FieldTypeHeading, FieldTypeParagraph,
		FieldTypeDivider, FieldTypeHidden, FieldTypeConsent:
		return true
	default:
		return false
	}
}

// GetFieldTypes returns all supported field types.
func GetFieldTypes() []string {
	return []string{
		FieldTypeText,
		FieldTypeEmail,
		FieldTypePhone,
		FieldTypeTextarea,
		FieldTypeSelect,
		FieldTypeRadio,
		FieldTypeCheckbox,
		FieldTypeDate,
		FieldTypeNumber,
		FieldTypeURL,
		FieldTypeHeading,
		FieldTypeParagraph,
		FieldTypeDivider,
		FieldTypeHidden,
		FieldTypeConsent,
	}
}
