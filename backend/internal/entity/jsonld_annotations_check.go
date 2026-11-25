package entity

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// AnnotationFindings is what CheckSchemaAnnotations found in a content type's JSON Schema.
type AnnotationFindings struct {
	RootType string // root x-schema-org-type; empty when the schema has none
	Errors   []string
	Warnings []string
}

var eventStatusTerms = []string{"EventScheduled", "EventCancelled", "EventPostponed", "EventRescheduled", "EventMovedOnline"}

// CheckSchemaAnnotations reports x-schema-org-enum and x-schema-org-datetime mappings the
// JSON-LD generator would ignore. It returns no findings for a schema that does not parse or
// has no root x-schema-org-type, because the generator does not read annotations then.
// The output order is deterministic.
func CheckSchemaAnnotations(schema jsontext.Value) AnnotationFindings {
	var def SchemaDefinition
	if err := jsonx.Unmarshal(schema, &def, jsonx.StoredLoose); err != nil || def.SchemaOrgType == "" {
		return AnnotationFindings{}
	}
	f := AnnotationFindings{RootType: def.SchemaOrgType}
	walkAnnotations("", def.Properties, &f)
	return f
}

// walkAnnotations checks every property of props in sorted key order and recurses into
// nested objects and array items. prefix is the path of the parent ("" at the root).
func walkAnnotations(prefix string, props map[string]*SchemaProperty, f *AnnotationFindings) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := props[k]
		if p == nil {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		checkPropertyAnnotations(path, p, props, f)
		if p.Properties != nil {
			walkAnnotations(path, p.Properties, f)
		}
		if p.Items != nil && p.Items.Properties != nil {
			walkAnnotations(path+"[]", p.Items.Properties, f)
		}
	}
}

func checkPropertyAnnotations(path string, p *SchemaProperty, siblings map[string]*SchemaProperty, f *AnnotationFindings) {
	errf := func(format string, a ...any) { f.Errors = append(f.Errors, fmt.Sprintf(format, a...)) }

	enumMap, hasEnum, enumOK := enumAnnotation(p)
	timeProp, hasTime, timeOK := datetimeAnnotation(p)

	if hasEnum {
		const pre = "x-schema-org-enum on %q: "
		if !enumOK {
			errf(pre+"must map enum values to schema.org URLs or terms", path)
		}
		allowed, hasList := enumValues(p)
		if !hasList {
			errf(pre+"the property has no enum", path)
		}
		keys := make([]string, 0, len(enumMap))
		for k := range enumMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if hasList && !slices.Contains(allowed, k) {
				errf(pre+"%q is not one of the enum values", path, k)
			}
			target, ok := schemaOrgTarget(enumMap[k])
			if !ok {
				errf(pre+"%q is not a schema.org URL or term", path, enumMap[k])
				continue
			}
			if p.SchemaOrgProperty == "eventStatus" && !slices.Contains(eventStatusTerms, strings.TrimPrefix(target, schemaOrgPrefix)) {
				f.Warnings = append(f.Warnings, fmt.Sprintf(pre+"%q is not an eventStatus value", path, enumMap[k]))
			}
		}
		if p.SchemaOrgProperty == "" {
			errf(pre+"the property has no x-schema-org-property", path)
		}
	}

	if hasTime {
		const pre = "x-schema-org-datetime on %q: "
		if !timeOK {
			errf(pre+`must be {"time": "<property>"}`, path)
		}
		if p.Type != "string" || p.Format != "date" {
			errf(pre+"the property is not a string with format date", path)
		}
		if timeOK {
			sib, ok := siblings[timeProp]
			switch {
			case !ok || sib == nil:
				errf(pre+"%q is not a property", path, timeProp)
			case sib.Type != "string":
				errf(pre+"%q is not a string", path, timeProp)
			case sib.SchemaOrgProperty != "":
				f.Warnings = append(f.Warnings, fmt.Sprintf(pre+"%q also prints on its own as %q", path, timeProp, sib.SchemaOrgProperty))
			}
		}
		if p.SchemaOrgProperty == "" {
			errf(pre+"the property has no x-schema-org-property", path)
		}
	}

	if hasEnum && hasTime {
		errf("%q: x-schema-org-enum and x-schema-org-datetime cannot be combined", path)
	}
}

// enumValues returns the string members of the JSON Schema enum; ok is false when the
// property has no enum array.
func enumValues(p *SchemaProperty) (vals []string, ok bool) {
	list, ok := p.Enum.([]any)
	if !ok {
		return nil, false
	}
	for _, v := range list {
		if s, isStr := v.(string); isStr {
			vals = append(vals, s)
		}
	}
	return vals, true
}
