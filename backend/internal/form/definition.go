package form

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
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
		if f.Link != "" && !validConsentLink(f.Link) {
			bad("Use a link starting with / or http(s)://")
		}
	}
	return errs.orNil()
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
