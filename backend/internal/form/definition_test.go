package form

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/spam"
)

func lg(id string) *FieldLogic { return &FieldLogic{FieldID: id, Op: LogicFilled} }

func TestValidateLogic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fields []FormField
		badIDs []string
	}{
		{"valid chain", []FormField{{ID: "a"}, {ID: "b", Logic: lg("a")}, {ID: "c", Logic: lg("b")}}, nil},
		{"self", []FormField{{ID: "a", Logic: lg("a")}}, []string{"a"}},
		{"missing", []FormField{{ID: "a", Logic: lg("zz")}}, []string{"a"}},
		{"empty source", []FormField{{ID: "a", Logic: &FieldLogic{Op: LogicIs}}}, []string{"a"}},
		{"unknown op", []FormField{{ID: "a"}, {ID: "b", Logic: &FieldLogic{FieldID: "a", Op: "nope"}}}, []string{"b"}},
		{"no id", []FormField{{ID: "a"}, {Logic: lg("a")}}, []string{""}},
		{"2-cycle", []FormField{{ID: "a", Logic: lg("b")}, {ID: "b", Logic: lg("a")}}, []string{"a"}},
		{"3-cycle with tail", []FormField{{ID: "t", Logic: lg("a")}, {ID: "a", Logic: lg("b")}, {ID: "b", Logic: lg("c")}, {ID: "c", Logic: lg("a")}}, []string{"a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := ValidateLogic(tc.fields)
			if len(errs) != len(tc.badIDs) {
				t.Fatalf("errors = %v, want %d", errs, len(tc.badIDs))
			}
			for i, id := range tc.badIDs {
				if errs[i].FieldID != id {
					t.Errorf("error %d field = %q, want %q", i, errs[i].FieldID, id)
				}
			}
			if len(errs) > 0 && !errors.Is(errs, ErrInvalidDefinition) {
				t.Error("errors.Is(ErrInvalidDefinition) = false")
			}
		})
	}
}

func TestValidateDefinition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		f    FormField
		ok   bool
	}{
		{"heading with text", FormField{ID: "a", Type: FieldTypeHeading, Text: "Hi"}, true},
		{"heading without text", FormField{ID: "a", Type: FieldTypeHeading}, false},
		{"divider required", FormField{ID: "a", Type: FieldTypeDivider, Required: true}, false},
		{"hidden static", FormField{ID: "a", Type: FieldTypeHidden, Name: "h", Hidden: &HiddenSpec{Source: "static", Value: "v"}}, true},
		{"hidden param needs name", FormField{ID: "a", Type: FieldTypeHidden, Name: "h", Hidden: &HiddenSpec{Source: "param"}}, false},
		{"hidden bad source", FormField{ID: "a", Type: FieldTypeHidden, Name: "h", Hidden: &HiddenSpec{Source: "cookie"}}, false},
		{"hidden missing spec", FormField{ID: "a", Type: FieldTypeHidden, Name: "h"}, false},
		{"spec on text", FormField{ID: "a", Type: FieldTypeText, Hidden: &HiddenSpec{Source: "page"}}, false},
		{"consent relative link", FormField{ID: "a", Type: FieldTypeConsent, Link: "/privacy"}, true},
		{"consent https link", FormField{ID: "a", Type: FieldTypeConsent, Link: "https://example.com/p"}, true},
		{"consent js link", FormField{ID: "a", Type: FieldTypeConsent, Link: "javascript:alert(1)"}, false},
		{"consent protocol-relative", FormField{ID: "a", Type: FieldTypeConsent, Link: "//evil.example"}, false},
		{"sloppy legacy text passes", FormField{ID: "a", Type: FieldTypeText, Name: "Bad Name"}, true},
	}
	for _, tc := range tests {
		errs := ValidateDefinition([]FormField{tc.f})
		if (len(errs) == 0) != tc.ok {
			t.Errorf("%s: errors = %v, want ok=%v", tc.name, errs, tc.ok)
		}
	}
}

func TestValidateDefinitionStrict(t *testing.T) {
	t.Parallel()
	ok := FormField{ID: "a", Type: FieldTypeText, Name: "name", Label: "Name"}
	pat := "("
	tests := []struct {
		name   string
		fields []FormField
		ok     bool
	}{
		{"valid", []FormField{ok}, true},
		{"sloppy name", []FormField{{ID: "a", Type: FieldTypeText, Name: "Bad Name", Label: "x"}}, false},
		{"duplicate name", []FormField{ok, {ID: "b", Type: FieldTypeText, Name: "name", Label: "x"}}, false},
		{"duplicate id", []FormField{ok, {ID: "a", Type: FieldTypeText, Name: "other", Label: "x"}}, false},
		{"empty id", []FormField{{Type: FieldTypeText, Name: "n", Label: "x"}}, false},
		{"select without options", []FormField{{ID: "a", Type: FieldTypeSelect, Name: "n", Label: "x"}}, false},
		{"tickbox without options", []FormField{{ID: "a", Type: FieldTypeCheckbox, Name: "n", Label: "x"}}, true},
		{"bad pattern", []FormField{{ID: "a", Type: FieldTypeText, Name: "n", Label: "x", Validation: &FieldValidation{Pattern: &pat}}}, false},
		{"bad width", []FormField{{ID: "a", Type: FieldTypeText, Name: "n", Label: "x", Width: "wide"}}, false},
		{"layout without name", []FormField{{ID: "a", Type: FieldTypeDivider}}, true},
		{"layout ids still unique", []FormField{{ID: "a", Type: FieldTypeDivider}, {ID: "a", Type: FieldTypeDivider}}, false},
	}
	for _, tc := range tests {
		errs := ValidateDefinitionStrict(tc.fields)
		if (len(errs) == 0) != tc.ok {
			t.Errorf("%s: errors = %v, want ok=%v", tc.name, errs, tc.ok)
		}
	}
	many := make([]FormField, 61)
	for i := range many {
		many[i] = FormField{ID: string(rune('A' + i)), Type: FieldTypeDivider}
	}
	if len(ValidateDefinitionStrict(many)) == 0 {
		t.Error("61 fields accepted")
	}
}

func TestSettingsProblems(t *testing.T) {
	t.Parallel()
	fields := []FormField{{Name: "email", Type: FieldTypeEmail}, {Name: "city", Type: FieldTypeText}}
	long := strings.Repeat("w", 101)
	tests := []struct {
		name string
		s    FormSettings
		want string // expected FieldID, "" = valid
	}{
		{"empty", FormSettings{}, ""},
		{"all valid", FormSettings{NotifySubject: "Hi {{email}}", NotifyReplyTo: "email", RateLimitPerHour: 5, RetentionDays: 90, BlockedWords: []string{"casino"}}, ""},
		{"retention", FormSettings{RetentionDays: 31}, "settings.retention_days"},
		{"rate high", FormSettings{RateLimitPerHour: 1001}, "settings.rate_limit_per_hour"},
		{"rate negative", FormSettings{RateLimitPerHour: -1}, "settings.rate_limit_per_hour"},
		{"too many words", FormSettings{BlockedWords: make([]string, spam.MaxBlockedWords+1)}, "settings.blocked_words"},
		{"long word", FormSettings{BlockedWords: []string{long}}, "settings.blocked_words"},
		{"subject crlf", FormSettings{NotifySubject: "a\r\nBcc: x"}, "settings.notify_subject"},
		{"subject long", FormSettings{ConfirmationSubject: strings.Repeat("s", 201)}, "settings.confirmation_subject"},
		{"reply-to not email", FormSettings{NotifyReplyTo: "city"}, "settings.notify_reply_to"},
		{"reply-to unknown", FormSettings{NotifyReplyTo: "zzz"}, "settings.notify_reply_to"},
		{"bad redirect", FormSettings{SuccessRedirectURL: "//evil"}, "settings.success_redirect_url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := settingsProblems(&tc.s, fields)
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v, want none", errs)
				}
				return
			}
			if len(errs) == 0 || errs[0].FieldID != tc.want {
				t.Fatalf("errors = %v, want %s", errs, tc.want)
			}
		})
	}
	if errs := settingsProblems(&FormSettings{NotifyReplyTo: "zzz"}, nil); len(errs) != 0 {
		t.Errorf("reply-to checked without fields: %v", errs)
	}
}
