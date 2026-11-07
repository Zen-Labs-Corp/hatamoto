package form

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/spam"
)

const (
	maxDefinitionFields = 60
	maxHiddenSpecRunes  = 100
)

var definitionNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ErrInvalidDefinition wraps a rejected form definition.
var ErrInvalidDefinition = errors.New("invalid form definition")

// DefinitionError is one problem in a form definition. FieldID is empty for
// form-level problems.
type DefinitionError struct {
	FieldID string `json:"field_id"`
	Message string `json:"message"`
}

func (e DefinitionError) Error() string { return e.Message }

// DefinitionErrors is the list of problems found in a definition.
type DefinitionErrors []DefinitionError

func (es DefinitionErrors) Error() string {
	if len(es) == 0 {
		return ErrInvalidDefinition.Error()
	}
	return ErrInvalidDefinition.Error() + ": " + es[0].Message
}

// Is makes errors.Is(err, ErrInvalidDefinition) true.
func (es DefinitionErrors) Is(target error) bool { return target == ErrInvalidDefinition }

// orNil returns nil for an empty list so callers can compare with nil.
func (es DefinitionErrors) orNil() DefinitionErrors {
	if len(es) == 0 {
		return nil
	}
	return es
}

// ValidateLogic checks every logic rule: the source must be another field of
// the form, the op must be known, and the rules must not form a cycle.
func ValidateLogic(fields []FormField) DefinitionErrors {
	var errs DefinitionErrors
	byID := make(map[string]*FormField, len(fields))
	for i := range fields {
		if fields[i].ID != "" {
			byID[fields[i].ID] = &fields[i]
		}
	}
	for i := range fields {
		f := &fields[i]
		l := f.Logic
		if l == nil {
			continue
		}
		bad := func(msg string) { errs = append(errs, DefinitionError{FieldID: f.ID, Message: msg}) }
		switch {
		case f.ID == "":
			bad("A field with logic needs an id")
		case l.FieldID == "":
			bad("Choose the field this rule depends on")
		case l.FieldID == f.ID:
			bad("A field cannot depend on itself")
		case byID[l.FieldID] == nil:
			bad("The field this rule depends on does not exist")
		case !validLogicOp(l.Op):
			bad("Unknown logic operator")
		}
	}
	if len(errs) > 0 {
		return errs
	}
	// Each field has at most one outgoing edge, so following the chain with
	// colour marks finds every cycle.
	const (
		onPath = 1
		done   = 2
	)
	mark := make(map[string]int, len(byID))
	for i := range fields {
		var path []string
		id := fields[i].ID
		for id != "" && mark[id] == 0 {
			mark[id] = onPath
			path = append(path, id)
			if l := byID[id].Logic; l != nil {
				id = l.FieldID
			} else {
				id = ""
			}
		}
		if id != "" && mark[id] == onPath {
			errs = append(errs, DefinitionError{FieldID: id, Message: "These rules depend on each other in a loop"})
		}
		for _, p := range path {
			mark[p] = done
		}
	}
	return errs.orNil()
}

// ValidateDefinition runs the checks for the logic, hidden, consent and
// layout parts of a definition. It leaves the older field rules to
// ValidateDefinitionStrict so existing callers with loose fields still pass.
func ValidateDefinition(fields []FormField) DefinitionErrors {
	errs := ValidateLogic(fields)
	for i := range fields {
		f := &fields[i]
		bad := func(msg string) { errs = append(errs, DefinitionError{FieldID: f.ID, Message: msg}) }
		if f.Hidden != nil && f.Type != FieldTypeHidden {
			bad("Only a hidden field can have a hidden source")
		}
		switch f.Type {
		case FieldTypeHidden:
			checkHiddenSpec(f, bad)
		case FieldTypeHeading, FieldTypeParagraph:
			if strings.TrimSpace(f.Text) == "" {
				bad("Enter the text to show")
			}
		}
		if f.IsLayout() || f.Type == FieldTypeHidden {
			if f.Required {
				bad("This field type cannot be required")
			}
			if len(f.Options) > 0 {
				bad("This field type has no choices")
			}
		}
		if f.Type == FieldTypeFile {
			checkFileSpec(f, bad)
		}
		if f.Link != "" && !validConsentLink(f.Link) {
			bad("Use a link starting with / or http(s)://")
		}
	}
	return errs.orNil()
}

// checkFileSpec holds a file field to the extensions in the table and the
// size ceiling. Text rules mean nothing on a file and are refused.
func checkFileSpec(f *FormField, bad func(string)) {
	v := f.Validation
	if v == nil || len(v.Accept) == 0 {
		bad("Choose at least one allowed file type")
		return
	}
	for _, ext := range v.Accept {
		if !ValidAcceptExt(ext) {
			bad("File type ." + strings.TrimPrefix(ext, ".") + " is not allowed")
			break
		}
	}
	if v.MaxMB != nil && (*v.MaxMB < 1 || *v.MaxMB > MaxFileUploadMB) {
		bad(fmt.Sprintf("Largest file must be between 1 and %d MB", MaxFileUploadMB))
	}
	if len(f.Options) > 0 || v.Pattern != nil || v.MinLength != nil || v.MaxLength != nil || v.Min != nil || v.Max != nil {
		bad("A file field takes only allowed types and a size limit")
	}
}

func checkHiddenSpec(f *FormField, bad func(string)) {
	h := f.Hidden
	if h == nil {
		bad("Choose where the hidden value comes from")
		return
	}
	switch h.Source {
	case "param":
		if strings.TrimSpace(h.Param) == "" {
			bad("Enter the name of the URL parameter")
		}
	case "page", "referrer", "static":
	default:
		bad("Unknown hidden value source")
	}
	if utf8.RuneCountInString(h.Param) > maxHiddenSpecRunes || utf8.RuneCountInString(h.Value) > maxHiddenSpecRunes {
		bad("Use at most 100 characters")
	}
}

func validConsentLink(s string) bool {
	if strings.HasPrefix(s, "/") {
		return !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, "\\\r\n")
	}
	return (strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) && !strings.ContainsAny(s, " \r\n")
}

// ValidateDefinitionStrict is ValidateDefinition plus the rules for a
// complete save: field names, ids, labels, options, patterns and widths.
func ValidateDefinitionStrict(fields []FormField) DefinitionErrors {
	errs := ValidateDefinition(fields)
	if len(fields) > maxDefinitionFields {
		errs = append(errs, DefinitionError{Message: "A form can have at most 60 fields"})
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for i := range fields {
		f := &fields[i]
		bad := func(msg string) { errs = append(errs, DefinitionError{FieldID: f.ID, Message: msg}) }
		switch {
		case f.ID == "":
			bad("Every field needs an id")
		case ids[f.ID]:
			bad("Another field already uses this id")
		}
		ids[f.ID] = true
		if !ValidFieldType(f.Type) {
			bad("Unknown field type")
		}
		if f.IsInput() {
			switch {
			case f.Name == "":
				bad("Enter a field name")
			case !definitionNameRe.MatchString(f.Name):
				bad("Start with a letter, then lowercase letters, numbers and underscores")
			case names[f.Name]:
				bad("Another field already uses this name")
			}
			names[f.Name] = true
			if f.Label == "" {
				bad("Enter a label")
			}
		}
		if utf8.RuneCountInString(f.Label) > 200 {
			bad("Use at most 200 characters")
		}
		if (f.Type == FieldTypeSelect || f.Type == FieldTypeRadio) && len(f.Options) == 0 {
			bad("Add at least one choice")
		}
		if p := f.Validation; p != nil && p.Pattern != nil && *p.Pattern != "" {
			if _, err := regexp.Compile(*p.Pattern); err != nil || len(*p.Pattern) > 500 {
				bad("This is not a valid pattern")
			}
		}
		switch f.Width {
		case "", "full", "half", "third":
		default:
			bad("Width must be full, half or third")
		}
	}
	return errs.orNil()
}

// ValidRedirectTarget reports whether u may be a success redirect: empty, a
// site-relative path, or an http(s) address.
func ValidRedirectTarget(u string) bool {
	if u == "" {
		return true
	}
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.ContainsAny(u, "\\ \t\r\n") {
		return true
	}
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != "" && !strings.ContainsAny(u, " \t\r\n\\")
}

const (
	maxRateLimitPerHour = 1000
	maxSubjectRunes     = 200
)

// settingsProblems lists what is wrong with a form's settings. A Reply-To that
// names no email field is not one: the Build tab can rename or retype that
// field at any time, so sending ignores it and the Form check flags it.
func settingsProblems(s *FormSettings) DefinitionErrors {
	if s == nil {
		return nil
	}
	var errs DefinitionErrors
	add := func(key, msg string) { errs = append(errs, DefinitionError{FieldID: "settings." + key, Message: msg}) }
	if !ValidRedirectTarget(s.SuccessRedirectURL) {
		add("success_redirect_url", "Enter a path like /thank-you or a full address starting with https://")
	}
	for _, sub := range []struct{ key, val string }{{"notify_subject", s.NotifySubject}, {"confirmation_subject", s.ConfirmationSubject}} {
		if utf8.RuneCountInString(sub.val) > maxSubjectRunes || strings.ContainsAny(sub.val, "\r\n") {
			add(sub.key, "Use one line of at most 200 characters")
		}
	}
	if s.RateLimitPerHour < 0 || s.RateLimitPerHour > maxRateLimitPerHour {
		add("rate_limit_per_hour", "Enter a number from 0 to 1000")
	}
	switch s.RetentionDays {
	case 0, 30, 90, 365:
	default:
		add("retention_days", "Choose 30 days, 90 days, 1 year or never")
	}
	if len(s.BlockedWords) > spam.MaxBlockedWords {
		add("blocked_words", fmt.Sprintf("Use at most %d words", spam.MaxBlockedWords))
	}
	for _, w := range s.BlockedWords {
		if len(w) > spam.MaxWordLen {
			add("blocked_words", fmt.Sprintf("Each word can be at most %d characters", spam.MaxWordLen))
			break
		}
	}
	return errs
}
