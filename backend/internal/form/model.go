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
)

// Common errors.
var (
	ErrFormNotFound       = errors.New("form not found")
	ErrSubmissionNotFound = errors.New("submission not found")
	ErrSlugAlreadyExists  = errors.New("form slug already exists")
	ErrInvalidStatus      = errors.New("invalid form status")
	ErrInvalidFieldType   = errors.New("invalid field type")
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
}

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
	StoreSubmissions    bool   `json:"store_submissions"`
}

// FormSubmission represents a submitted form entry.
type FormSubmission struct {
	ID        uuid.UUID          `json:"id"`
	FormID    uuid.UUID          `json:"form_id"`
	Data      map[string]any     `json:"data"`
	Metadata  SubmissionMetadata `json:"metadata"`
	Status    string             `json:"status"`
	CreatedAt time.Time          `json:"created_at"`

	// Populated on read
	FormName *string `json:"form_name,omitempty"`
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

// CreateSubmissionInput represents input for creating a form submission.
type CreateSubmissionInput struct {
	Data     map[string]any     `json:"data"`
	Metadata SubmissionMetadata `json:"metadata"`
	// Status is the initial status; empty means SubmissionStatusNew. Set by
	// the server (spam pipeline), never read from a request body.
	Status string `json:"-"`
}

// UpdateSubmissionInput represents input for updating a submission.
type UpdateSubmissionInput struct {
	Status *string `json:"status,omitempty"`
}

// ListSubmissionsParams represents parameters for listing submissions.
type ListSubmissionsParams struct {
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
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
		FieldTypeNumber, FieldTypeURL:
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
	}
}
