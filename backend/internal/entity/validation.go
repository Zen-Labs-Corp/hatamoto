package entity

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Validator validates entity attributes against JSON schemas.
type Validator struct {
	compiler *jsonschema.Compiler
}

// NewValidator creates a new JSON Schema validator.
func NewValidator() *Validator {
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	installLenientFormats(compiler)
	return &Validator{
		compiler: compiler,
	}
}

// lenientURI accepts:
//   - empty strings (treated by `format` keyword as valid anyway in some setups,
//     but we make it explicit so optional fields aren't tripped up by empty input)
//   - any single-leading-slash relative path (e.g. /media/.../foo.png) — these
//     are internal-host URLs the frontend renders directly
//   - any http(s):// absolute URL whose body contains no whitespace
//
// The strict RFC-3986 check the library ships with rejects characters like
// `•` that are perfectly common in human-generated filenames imported from
// WordPress. Browsers percent-encode them at render time, so they are
// functionally valid hyperlinks. Loosening this check eliminates a class of
// "can't save existing entity" errors without weakening anything that matters.
func lenientURI(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	if s == "" {
		return true
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	if strings.HasPrefix(s, "/") {
		// Reject protocol-relative `//host` — same defence as on the lead
		// redirect side. Forces internal paths to be host-relative.
		if strings.HasPrefix(s, "//") {
			return false
		}
		return true
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return len(s) > 7
	}
	// Allow mailto:, tel:, and other documented schemes for CTAs.
	if i := strings.Index(s, ":"); i > 0 && i < 16 {
		return true
	}
	return false
}

// installLenientFormats overrides the strict "uri" / "uri-reference" /
// "url" / "iri" formats on the supplied compiler with the lenient checker
// above. Apply this to every Compiler we instantiate so all validation
// paths agree on what counts as a URI.
func installLenientFormats(c *jsonschema.Compiler) {
	c.Formats["uri"] = lenientURI
	c.Formats["uri-reference"] = lenientURI
	c.Formats["url"] = lenientURI
	c.Formats["iri"] = lenientURI
	c.Formats["iri-reference"] = lenientURI
}

// ValidateSchema validates that a JSON schema is itself valid.
//
// It performs three checks in order:
//  1. The bytes parse as JSON and the root has a `type`.
//  2. The shape is something the Phase 3 visual builder can edit
//     (ValidateSchemaShape) — bounded primitive set, every property
//     typed, nested objects/arrays well-formed, no surprises that
//     would crash the editor at render time.
//  3. The schema compiles as JSON Schema Draft 2020-12 so the
//     entity validator can later use it to check instances.
//
// Any failure returns ErrInvalidSchema wrapped with a path-prefixed
// reason so the admin UI can surface "properties.foo.type missing"
// rather than a generic compile error.
func (v *Validator) ValidateSchema(schemaJSON json.RawMessage) error {
	if len(schemaJSON) == 0 {
		return fmt.Errorf("%w: empty schema", ErrInvalidSchema)
	}

	// Parse the schema to check it's valid JSON
	var schemaMap map[string]any
	if err := json.Unmarshal(schemaJSON, &schemaMap); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}

	// Ensure it has a type field
	if _, ok := schemaMap["type"]; !ok {
		return fmt.Errorf("%w: schema must have a 'type' field", ErrInvalidSchema)
	}

	// Editor-shape check — keeps the Phase 3 builder from ever
	// receiving a schema it cannot render. Runs before compile so
	// the more actionable error wins.
	if err := ValidateSchemaShape(schemaJSON); err != nil {
		return err
	}

	// Try to compile the schema to validate it
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	installLenientFormats(compiler)

	if err := compiler.AddResource("schema.json", strings.NewReader(string(schemaJSON))); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}

	if _, err := compiler.Compile("schema.json"); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}

	return nil
}

// supportedSchemaTypes is the closed set of JSON Schema `type` values
// the visual editor (and our FieldRenderer on the frontend) knows how
// to render. Any other type at write time means the admin hand-edited
// JSON to introduce something we cannot show — reject it so they fix
// the JSON rather than discovering it later as a broken field.
var supportedSchemaTypes = map[string]struct{}{
	"string":  {},
	"number":  {},
	"integer": {},
	"boolean": {},
	"array":   {},
	"object":  {},
}

// reservedPropertyNames is a denylist of property names that would
// pollute the JavaScript object prototype if they ever round-tripped
// into a `{...attrs}` spread or `Object.assign({}, attrs)` on the
// frontend. The propertyNameRe regex below accidentally permits all
// of these (they're valid `[A-Za-z_][A-Za-z0-9_]*` identifiers), so
// we layer an explicit denylist on top to defang the prototype-
// pollution vector at the storage boundary.
var reservedPropertyNames = map[string]struct{}{
	"__proto__":            {},
	"constructor":          {},
	"prototype":            {},
	"hasOwnProperty":       {},
	"isPrototypeOf":        {},
	"propertyIsEnumerable": {},
	"toString":             {},
	"valueOf":              {},
	"__defineGetter__":     {},
	"__defineSetter__":     {},
	"__lookupGetter__":     {},
	"__lookupSetter__":     {},
}

// maxSchemaBytes caps the size of any user-authored schema. 64 KiB is
// a comfortable ceiling for hand-authored shapes (the largest real
// schema is ~12 KiB) and rejects the pathological cases that turn
// schema compilation into a DoS vector. Enforced as the first guard
// in ValidateSchemaShape so oversized payloads never even parse.
const maxSchemaBytes = 64 * 1024

// ValidateSchemaShape enforces editor-renderability invariants on a
// user-authored JSON Schema. It is intentionally narrower than the
// JSON Schema spec — it codifies what the visual schema builder and
// the field-renderer can round-trip without surprise.
//
// Rules enforced:
//   - Payload is at most maxSchemaBytes (64 KiB) — anti-DoS guard.
//   - Root parses as a JSON object.
//   - Root has a `type` field and the value is one of supportedSchemaTypes.
//   - If root type is `object`, `properties` must be present and an object.
//   - Every property has a `type` of a supported kind.
//   - Nested `object` properties recurse with the same rules.
//   - `array` properties must declare `items` that is itself a valid
//     property shape, AND items.type must NOT be `array` (repeaters
//     are single-level only by design — the UI cannot render nested
//     repeaters, and the raw-JSON escape hatch must agree).
//   - `additionalProperties` and `patternProperties` are rejected
//     outright — both bypass the closed-shape contract that the field
//     renderer and downstream consumers rely on. Admins should add
//     fields via `properties`, not escape hatches.
//   - Property names match `^[A-Za-z_][A-Za-z0-9_]*$` AND are not on
//     the prototype-pollution denylist (`__proto__`, `constructor`,
//     etc.).
//
// Errors are wrapped with ErrInvalidSchema and include a dotted path
// (e.g. `properties.faq.items.type missing`) so the admin builder can
// highlight the offending field.
func ValidateSchemaShape(raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty schema", ErrInvalidSchema)
	}
	if len(raw) > maxSchemaBytes {
		return fmt.Errorf("%w: schema too large: %d bytes exceeds %d-byte limit", ErrInvalidSchema, len(raw), maxSchemaBytes)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	return validateShapeNode(root, "")
}

// propertyNameRe is intentionally strict: snake_case or camelCase,
// must start with a letter or underscore. Matches the convention used
// across the existing schemas (`featured_image`, `page_sections`).
var propertyNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateShapeNode validates a single schema node and recurses into
// its children. `path` is dotted (`properties.foo.items`) and is
// surfaced verbatim in error messages so the admin canvas can point
// at the bad field.
func validateShapeNode(node map[string]any, path string) error {
	typVal, ok := node["type"]
	if !ok {
		return fmt.Errorf("%w: %s missing 'type'", ErrInvalidSchema, pathOrRoot(path))
	}
	typStr, ok := typVal.(string)
	if !ok {
		return fmt.Errorf("%w: %s 'type' must be a string", ErrInvalidSchema, pathOrRoot(path))
	}
	if _, ok := supportedSchemaTypes[typStr]; !ok {
		return fmt.Errorf("%w: %s unsupported type %q (allowed: string, number, integer, boolean, array, object)", ErrInvalidSchema, pathOrRoot(path), typStr)
	}

	switch typStr {
	case "object":
		// Closed-shape contract: reject `additionalProperties` and
		// `patternProperties` outright. Both bypass the schema-builder
		// invariant that every accepted field appears in `properties`.
		// Admins should add fields via the builder; the raw-JSON path
		// is for power users, not for opening escape hatches.
		if _, present := node["additionalProperties"]; present {
			return fmt.Errorf("%w: %s 'additionalProperties' is not allowed (closed shapes only — add fields via 'properties')", ErrInvalidSchema, pathOrRoot(path))
		}
		if _, present := node["patternProperties"]; present {
			return fmt.Errorf("%w: %s 'patternProperties' is not allowed (closed shapes only — add fields via 'properties')", ErrInvalidSchema, pathOrRoot(path))
		}
		propsAny, hasProps := node["properties"]
		if !hasProps {
			return fmt.Errorf("%w: %s of type 'object' missing 'properties'", ErrInvalidSchema, pathOrRoot(path))
		}
		props, ok := propsAny.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: %s 'properties' must be an object", ErrInvalidSchema, pathOrRoot(path))
		}
		for name, child := range props {
			if _, reserved := reservedPropertyNames[name]; reserved {
				return fmt.Errorf("%w: property name %q is reserved", ErrInvalidSchema, name)
			}
			if !propertyNameRe.MatchString(name) {
				return fmt.Errorf("%w: property name %q must match ^[A-Za-z_][A-Za-z0-9_]*$", ErrInvalidSchema, name)
			}
			childMap, ok := child.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: properties.%s must be an object", ErrInvalidSchema, name)
			}
			childPath := joinPath(path, "properties."+name)
			if err := validateShapeNode(childMap, childPath); err != nil {
				return err
			}
		}
		// `required` must be a list of strings that all exist in `properties`.
		if reqAny, ok := node["required"]; ok {
			reqList, ok := reqAny.([]any)
			if !ok {
				return fmt.Errorf("%w: %s 'required' must be an array", ErrInvalidSchema, pathOrRoot(path))
			}
			for _, item := range reqList {
				name, ok := item.(string)
				if !ok {
					return fmt.Errorf("%w: %s 'required' entries must be strings", ErrInvalidSchema, pathOrRoot(path))
				}
				if _, present := props[name]; !present {
					return fmt.Errorf("%w: %s 'required' references unknown property %q", ErrInvalidSchema, pathOrRoot(path), name)
				}
			}
		}

	case "array":
		itemsAny, hasItems := node["items"]
		if !hasItems {
			return fmt.Errorf("%w: %s of type 'array' missing 'items'", ErrInvalidSchema, pathOrRoot(path))
		}
		itemsMap, ok := itemsAny.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: %s 'items' must be an object", ErrInvalidSchema, pathOrRoot(path))
		}
		// Repeaters are single-level by design. A nested array would
		// require a recursive renderer the visual builder doesn't
		// support, and the raw-JSON path must agree so the two surfaces
		// stay in lock-step.
		if itemsType, ok := itemsMap["type"].(string); ok && itemsType == "array" {
			return fmt.Errorf("%w: %s 'array' not allowed (nested repeater)", ErrInvalidSchema, joinPath(path, "items.type"))
		}
		if err := validateShapeNode(itemsMap, joinPath(path, "items")); err != nil {
			return err
		}

	case "string":
		// enum, if present, must be a non-empty array of strings.
		if enumAny, ok := node["enum"]; ok {
			enumList, ok := enumAny.([]any)
			if !ok {
				return fmt.Errorf("%w: %s 'enum' must be an array", ErrInvalidSchema, pathOrRoot(path))
			}
			if len(enumList) == 0 {
				return fmt.Errorf("%w: %s 'enum' must contain at least one option", ErrInvalidSchema, pathOrRoot(path))
			}
			for i, opt := range enumList {
				if _, ok := opt.(string); !ok {
					return fmt.Errorf("%w: %s 'enum[%d]' must be a string", ErrInvalidSchema, pathOrRoot(path), i)
				}
			}
		}
	}
	return nil
}

func pathOrRoot(p string) string {
	if p == "" {
		return "root"
	}
	return p
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// ValidateAttributes validates entity attributes against a schema.
func (v *Validator) ValidateAttributes(schemaJSON, attributesJSON json.RawMessage) error {
	if len(schemaJSON) == 0 {
		return fmt.Errorf("%w: empty schema", ErrValidationFailed)
	}

	if len(attributesJSON) == 0 {
		// Empty attributes - treat as empty object
		attributesJSON = json.RawMessage(`{}`)
	}

	// Compile the schema
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	installLenientFormats(compiler)

	if err := compiler.AddResource("schema.json", strings.NewReader(string(schemaJSON))); err != nil {
		return fmt.Errorf("%w: failed to parse schema: %v", ErrValidationFailed, err)
	}

	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("%w: failed to compile schema: %v", ErrValidationFailed, err)
	}

	// Parse attributes
	var attributes any
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return fmt.Errorf("%w: invalid JSON in attributes: %v", ErrValidationFailed, err)
	}

	// Validate
	if err := schema.Validate(attributes); err != nil {
		validationErr, ok := err.(*jsonschema.ValidationError)
		if ok {
			return fmt.Errorf("%w: %s", ErrValidationFailed, formatValidationError(validationErr))
		}
		return fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}

	return nil
}

// formatValidationError formats a JSON schema validation error into a readable message.
func formatValidationError(err *jsonschema.ValidationError) string {
	var messages []string

	// Collect all error messages
	collectErrors(err, &messages, "")

	if len(messages) == 0 {
		return err.Error()
	}

	return strings.Join(messages, "; ")
}

// collectErrors recursively collects validation error messages.
func collectErrors(err *jsonschema.ValidationError, messages *[]string, path string) {
	currentPath := path
	if err.InstanceLocation != "" {
		currentPath = err.InstanceLocation
	}

	if err.Message != "" {
		msg := err.Message
		if currentPath != "" {
			msg = fmt.Sprintf("%s: %s", currentPath, msg)
		}
		*messages = append(*messages, msg)
	}

	for _, cause := range err.Causes {
		collectErrors(cause, messages, currentPath)
	}
}

// ValidationResult holds detailed validation results.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ValidationError represents a single validation error.
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	// Keyword is the JSON Schema keyword that failed ("required", "maxLength",
	// ...), set on the leaf errors; the admin forms map it to friendly text.
	Keyword string `json:"keyword,omitempty"`
}

// ValidateWithDetails validates attributes and returns detailed results.
func (v *Validator) ValidateWithDetails(schemaJSON, attributesJSON json.RawMessage) *ValidationResult {
	result := &ValidationResult{Valid: true}

	if len(schemaJSON) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Path:    "",
			Message: "empty schema",
		})
		return result
	}

	if len(attributesJSON) == 0 {
		attributesJSON = json.RawMessage(`{}`)
	}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	installLenientFormats(compiler)

	if err := compiler.AddResource("schema.json", strings.NewReader(string(schemaJSON))); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Path:    "",
			Message: fmt.Sprintf("failed to parse schema: %v", err),
		})
		return result
	}

	schema, err := compiler.Compile("schema.json")
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Path:    "",
			Message: fmt.Sprintf("failed to compile schema: %v", err),
		})
		return result
	}

	var attributes any
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Path:    "",
			Message: fmt.Sprintf("invalid JSON: %v", err),
		})
		return result
	}

	if err := schema.Validate(attributes); err != nil {
		result.Valid = false
		validationErr, ok := err.(*jsonschema.ValidationError)
		if ok {
			collectDetailedErrors(validationErr, &result.Errors)
		} else {
			result.Errors = append(result.Errors, ValidationError{
				Path:    "",
				Message: err.Error(),
			})
		}
	}

	return result
}

// collectDetailedErrors recursively collects validation errors with paths.
func collectDetailedErrors(err *jsonschema.ValidationError, errors *[]ValidationError) {
	if err.Message != "" {
		ve := ValidationError{
			Path:    err.InstanceLocation,
			Message: err.Message,
		}
		if len(err.Causes) == 0 {
			if i := strings.LastIndex(err.KeywordLocation, "/"); i >= 0 {
				ve.Keyword = err.KeywordLocation[i+1:]
			}
		}
		*errors = append(*errors, ve)
	}

	for _, cause := range err.Causes {
		collectDetailedErrors(cause, errors)
	}
}
