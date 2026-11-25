package entity

import (
	"strings"
	"time"
)

const schemaOrgPrefix = "https://schema.org/"

// enumAnnotation reads x-schema-org-enum. present reports that the key is set;
// wellFormed that it is an object whose values are all strings. m holds the
// string entries either way.
func enumAnnotation(p *SchemaProperty) (m map[string]string, present, wellFormed bool) {
	if p == nil || p.SchemaOrgEnum == nil {
		return nil, false, false
	}
	raw, ok := p.SchemaOrgEnum.(map[string]any)
	if !ok {
		return nil, true, false
	}
	m = make(map[string]string, len(raw))
	wellFormed = true
	for k, v := range raw {
		s, isStr := v.(string)
		if !isStr {
			wellFormed = false
			continue
		}
		m[k] = s
	}
	return m, true, wellFormed
}

// datetimeAnnotation reads x-schema-org-datetime, an object {"time": "<sibling>"}.
// wellFormed needs a non-empty string time property name.
func datetimeAnnotation(p *SchemaProperty) (timeProp string, present, wellFormed bool) {
	if p == nil || p.SchemaOrgDatetime == nil {
		return "", false, false
	}
	raw, ok := p.SchemaOrgDatetime.(map[string]any)
	if !ok {
		return "", true, false
	}
	name, ok := raw["time"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", true, false
	}
	return name, true, true
}

// schemaOrgTarget accepts a full https://schema.org/<Term> URL as is, or a bare
// <Term> that it expands. A term is a letter followed by letters and digits.
func schemaOrgTarget(s string) (string, bool) {
	s = strings.TrimSpace(s)
	term := strings.TrimPrefix(s, schemaOrgPrefix)
	if term == "" || !isSchemaOrgTerm(term) {
		return "", false
	}
	return schemaOrgPrefix + term, true
}

func isSchemaOrgTerm(t string) bool {
	for i := 0; i < len(t); i++ {
		c := t[i]
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return t != ""
}

// datetimeValue joins a date attribute and a time attribute into one ISO 8601
// value in loc (UTC when nil). The bool is false when nothing should print.
//
//   - date already RFC 3339: as stored
//   - no time (missing, nil or empty): a YYYY-MM-DD date as stored
//   - date YYYY-MM-DD and time HH:MM[:SS]: combined, with the offset of loc on that date
//   - valid date, invalid or non-string time: the date only
//   - invalid date, with or without a time: nothing
func datetimeValue(date string, clock any, loc *time.Location) (string, bool) {
	date = strings.TrimSpace(date)
	if date == "" {
		return "", false
	}
	if loc == nil {
		loc = time.UTC
	}
	clockStr, isStr := clock.(string)
	clockStr = strings.TrimSpace(clockStr)
	if _, err := time.Parse(time.RFC3339, date); err == nil {
		return date, true
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", false
	}
	if clock == nil || (isStr && clockStr == "") {
		return date, true
	}
	if !isStr {
		return date, true
	}
	t, err := time.Parse("15:04:05", clockStr)
	if err != nil {
		if t, err = time.Parse("15:04", clockStr); err != nil {
			return date, true
		}
	}
	return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc).
		Format("2006-01-02T15:04:05-07:00"), true
}

// mapAnnotatedScalar handles a property carrying x-schema-org-enum or
// x-schema-org-datetime and reports true; false means the property is not
// annotated and the caller runs the existing mapping. A handled property
// prints a value or nothing, never the raw stored value. Datetime wins over
// enum. A malformed annotation degrades: for an enum only its string entries
// count, for a datetime without a usable time name a readable date prints as stored.
func mapAnnotatedScalar(jsonLD, attrs map[string]any, ldKey string, attrValue any, prop *SchemaProperty, loc *time.Location) bool {
	if timeProp, present, wellFormed := datetimeAnnotation(prop); present {
		date, ok := attrValue.(string)
		if !ok {
			return true
		}
		var clock any
		if wellFormed {
			clock = attrs[timeProp]
		}
		if v, ok := datetimeValue(date, clock, loc); ok {
			jsonLD[ldKey] = v
		}
		return true
	}
	if m, present, _ := enumAnnotation(prop); present {
		key, ok := attrValue.(string)
		if !ok {
			return true
		}
		target, mapped := m[key]
		if !mapped {
			return true
		}
		if url, valid := schemaOrgTarget(target); valid {
			jsonLD[ldKey] = url
		}
		return true
	}
	return false
}
