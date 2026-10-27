package form

import (
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxAnswerRunes bounds an answer that sets no longest-answer check.
const maxAnswerRunes = 20000

// ValidateSubmission checks a public submission against the form's fields. It
// is the one validator behind the JSON and the urlencoded submit endpoints.
//
// Only answers to fields the form defines are returned: unknown keys are
// ignored (never stored). Empty optional answers are dropped. The errors are
// keyed by field name; the returned data is nil when there is any error.
func ValidateSubmission(fields []FormField, data map[string]any) (map[string]any, map[string]string) {
	clean := make(map[string]any, len(fields))
	errs := map[string]string{}
	for i := range fields {
		f := &fields[i]
		if f.Name == "" {
			continue
		}
		val, msg := validateAnswer(f, data[f.Name])
		switch {
		case msg != "":
			errs[f.Name] = msg
		case val != nil:
			clean[f.Name] = val
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return clean, nil
}

func isEmptyAnswer(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// validateAnswer returns the normalised answer (nil when there is none) or an
// error message for the field.
func validateAnswer(f *FormField, raw any) (any, string) {
	label := f.Label
	if label == "" {
		label = f.Name
	}
	// A checkbox without choices is a yes/no box: false counts as not ticked.
	if f.Type == FieldTypeCheckbox && len(f.Options) == 0 {
		return validateTickbox(f, label, raw)
	}
	if isEmptyAnswer(raw) {
		if f.Required {
			return nil, label + " is required"
		}
		return nil, ""
	}
	switch f.Type {
	case FieldTypeSelect, FieldTypeRadio:
		s, ok := raw.(string)
		if !ok || !hasOption(f.Options, s) {
			return nil, "Choose one of the listed options for " + label
		}
		return s, ""
	case FieldTypeCheckbox:
		list, ok := raw.([]any)
		if !ok {
			list = []any{raw}
		}
		out := make([]any, 0, len(list))
		for _, it := range list {
			s, ok := it.(string)
			if !ok || !hasOption(f.Options, s) {
				return nil, "Choose only the listed options for " + label
			}
			out = append(out, s)
		}
		return out, ""
	case FieldTypeNumber:
		return validateNumber(f, label, raw)
	}
	s, ok := raw.(string)
	if !ok {
		return nil, label + " must be text"
	}
	s = strings.TrimSpace(s)
	if msg := checkText(f, label, s); msg != "" {
		return nil, msg
	}
	return s, ""
}

func validateTickbox(f *FormField, label string, raw any) (any, string) {
	ticked := false
	switch x := raw.(type) {
	case bool:
		ticked = x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "", "0", "false", "off", "no":
		default:
			ticked = true
		}
	case nil:
	default:
		return nil, label + " must be yes or no"
	}
	if !ticked {
		if f.Required {
			return nil, label + " is required"
		}
		return nil, ""
	}
	return true, ""
}

func hasOption(opts []FieldOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

func validateNumber(f *FormField, label string, raw any) (any, string) {
	var n float64
	switch x := raw.(type) {
	case float64:
		n = x
	case string:
		var err error
		if n, err = strconv.ParseFloat(strings.TrimSpace(x), 64); err != nil {
			return nil, label + " must be a number"
		}
	default:
		return nil, label + " must be a number"
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, label + " must be a number"
	}
	if v := f.Validation; v != nil {
		if (v.Min != nil && n < float64(*v.Min)) || (v.Max != nil && n > float64(*v.Max)) {
			return nil, ruleMessage(f, fmt.Sprintf("%s is out of range", label))
		}
	}
	return n, ""
}

// checkText applies the type check, the length limits and the pattern.
func checkText(f *FormField, label, s string) string {
	n := utf8.RuneCountInString(s)
	switch f.Type {
	case FieldTypeEmail:
		if a, err := mail.ParseAddress(s); err != nil || a.Address != s || len(s) > 254 {
			return label + " must be a valid email address"
		}
	case FieldTypeURL:
		if u, err := url.Parse(s); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return label + " must be a web address starting with http:// or https://"
		}
	case FieldTypeDate:
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return label + " must be a date like 2026-01-31"
		}
	}
	v := f.Validation
	if v == nil {
		if n > maxAnswerRunes {
			return label + " is too long"
		}
		return ""
	}
	if v.MinLength != nil && n < *v.MinLength {
		return ruleMessage(f, fmt.Sprintf("%s must be at least %d characters", label, *v.MinLength))
	}
	if (v.MaxLength != nil && n > *v.MaxLength) || (v.MaxLength == nil && n > maxAnswerRunes) {
		return ruleMessage(f, label+" is too long")
	}
	if v.Pattern != nil && *v.Pattern != "" {
		// Like an HTML pattern, the answer must match as a whole.
		re, err := regexp.Compile(`^(?:` + *v.Pattern + `)$`)
		if err == nil && !re.MatchString(s) {
			return ruleMessage(f, label+" is not in the expected format")
		}
	}
	return ""
}

// ruleMessage is the editor's own message when set, else the default.
func ruleMessage(f *FormField, def string) string {
	if f.Validation != nil && f.Validation.Message != nil && strings.TrimSpace(*f.Validation.Message) != "" {
		return *f.Validation.Message
	}
	return def
}
