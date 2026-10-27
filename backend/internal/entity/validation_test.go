package entity

import (
	"encoding/json"
	"testing"
)

func TestValidator_ValidateSchema(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		name    string
		schema  string
		wantErr bool
	}{
		{
			name:    "valid object schema",
			schema:  `{"type": "object", "properties": {"name": {"type": "string"}}}`,
			wantErr: false,
		},
		{
			name:    "valid string schema",
			schema:  `{"type": "string", "minLength": 1}`,
			wantErr: false,
		},
		{
			name:    "empty schema",
			schema:  ``,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			schema:  `{invalid}`,
			wantErr: true,
		},
		{
			name:    "missing type field",
			schema:  `{"properties": {"name": {"type": "string"}}}`,
			wantErr: true,
		},
		{
			name:    "object missing properties",
			schema:  `{"type": "object"}`,
			wantErr: true,
		},
		{
			name:    "property missing type",
			schema:  `{"type": "object", "properties": {"foo": {"title": "Foo"}}}`,
			wantErr: true,
		},
		{
			name:    "unsupported property type",
			schema:  `{"type": "object", "properties": {"foo": {"type": "null"}}}`,
			wantErr: true,
		},
		{
			name:    "array without items",
			schema:  `{"type": "object", "properties": {"foo": {"type": "array"}}}`,
			wantErr: true,
		},
		{
			name:    "array with valid string items",
			schema:  `{"type": "object", "properties": {"tags": {"type": "array", "items": {"type": "string"}}}}`,
			wantErr: false,
		},
		{
			name:    "array with object items",
			schema:  `{"type": "object", "properties": {"faqs": {"type": "array", "items": {"type": "object", "properties": {"q": {"type": "string"}, "a": {"type": "string"}}}}}}`,
			wantErr: false,
		},
		{
			name:    "enum with empty options rejected",
			schema:  `{"type": "object", "properties": {"status": {"type": "string", "enum": []}}}`,
			wantErr: true,
		},
		{
			name:    "enum with valid options",
			schema:  `{"type": "object", "properties": {"status": {"type": "string", "enum": ["draft", "published"]}}}`,
			wantErr: false,
		},
		{
			name:    "required references unknown property",
			schema:  `{"type": "object", "required": ["ghost"], "properties": {"name": {"type": "string"}}}`,
			wantErr: true,
		},
		{
			name:    "invalid property name with hyphen",
			schema:  `{"type": "object", "properties": {"bad-name": {"type": "string"}}}`,
			wantErr: true,
		},
		{
			name:    "nested object properties",
			schema:  `{"type": "object", "properties": {"meta": {"type": "object", "properties": {"author": {"type": "string"}}}}}`,
			wantErr: false,
		},
		// --- Prototype-pollution denylist (regex permits these as
		// identifiers; explicit denylist must reject them so they
		// can't pollute downstream `{...attrs}` spreads on the client). ---
		{
			name:    "reserved property name __proto__ rejected",
			schema:  `{"type": "object", "properties": {"__proto__": {"type": "string"}}}`,
			wantErr: true,
		},
		{
			name:    "reserved property name constructor rejected",
			schema:  `{"type": "object", "properties": {"constructor": {"type": "string"}}}`,
			wantErr: true,
		},
		// --- Closed-shape contract: bypass-vectors rejected. ---
		{
			name:    "nested array (repeater of repeater) rejected",
			schema:  `{"type": "object", "properties": {"matrix": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}}}}`,
			wantErr: true,
		},
		{
			name:    "additionalProperties rejected (any value)",
			schema:  `{"type": "object", "properties": {"name": {"type": "string"}}, "additionalProperties": false}`,
			wantErr: true,
		},
		{
			name:    "patternProperties rejected",
			schema:  `{"type": "object", "properties": {"name": {"type": "string"}}, "patternProperties": {".*": {"type": "string"}}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateSchema(json.RawMessage(tt.schema))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSchema() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateSchemaShape_SizeCap verifies the 64 KiB guard rejects
// pathologically large schemas before they hit the parser. Cheap
// defence against DoS-by-large-schema since the admin surface is
// authenticated but not all admin sessions are equally trustworthy.
func TestValidateSchemaShape_SizeCap(t *testing.T) {
	// Build a schema that's syntactically valid but > 64 KiB. A long
	// `description` string lets us inflate the payload without
	// tripping any of the other shape rules.
	padding := make([]byte, 65*1024)
	for i := range padding {
		padding[i] = 'x'
	}
	oversized := []byte(`{"type":"object","description":"` + string(padding) + `","properties":{"name":{"type":"string"}}}`)

	if err := ValidateSchemaShape(json.RawMessage(oversized)); err == nil {
		t.Errorf("ValidateSchemaShape() with %d-byte payload: want error, got nil", len(oversized))
	}
}

func TestValidator_ValidateAttributes(t *testing.T) {
	v := NewValidator()

	productSchema := json.RawMessage(`{
		"type": "object",
		"required": ["name", "price"],
		"properties": {
			"name": {"type": "string", "minLength": 1},
			"price": {"type": "number", "minimum": 0},
			"description": {"type": "string"}
		}
	}`)

	tests := []struct {
		name       string
		schema     json.RawMessage
		attributes string
		wantErr    bool
	}{
		{
			name:       "valid attributes",
			schema:     productSchema,
			attributes: `{"name": "Widget", "price": 99.99}`,
			wantErr:    false,
		},
		{
			name:       "valid with optional field",
			schema:     productSchema,
			attributes: `{"name": "Widget", "price": 99.99, "description": "A great widget"}`,
			wantErr:    false,
		},
		{
			name:       "missing required field",
			schema:     productSchema,
			attributes: `{"name": "Widget"}`,
			wantErr:    true,
		},
		{
			name:       "wrong type for price",
			schema:     productSchema,
			attributes: `{"name": "Widget", "price": "not a number"}`,
			wantErr:    true,
		},
		{
			name:       "negative price",
			schema:     productSchema,
			attributes: `{"name": "Widget", "price": -5}`,
			wantErr:    true,
		},
		{
			name:       "empty name",
			schema:     productSchema,
			attributes: `{"name": "", "price": 10}`,
			wantErr:    true,
		},
		{
			name:       "empty attributes treated as empty object",
			schema:     productSchema,
			attributes: ``,
			wantErr:    true, // Required fields missing
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateAttributes(tt.schema, json.RawMessage(tt.attributes))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAttributes() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidator_ValidateWithDetails(t *testing.T) {
	v := NewValidator()

	schema := json.RawMessage(`{
		"type": "object",
		"required": ["email"],
		"properties": {
			"email": {"type": "string", "format": "email"},
			"age": {"type": "integer", "minimum": 0, "maximum": 150}
		}
	}`)

	t.Run("valid attributes returns no errors", func(t *testing.T) {
		attrs := json.RawMessage(`{"email": "test@example.com", "age": 25}`)
		result := v.ValidateWithDetails(schema, attrs)
		if !result.Valid {
			t.Errorf("expected valid, got errors: %v", result.Errors)
		}
	})

	t.Run("invalid attributes returns detailed errors", func(t *testing.T) {
		attrs := json.RawMessage(`{"age": -5}`)
		result := v.ValidateWithDetails(schema, attrs)
		if result.Valid {
			t.Error("expected invalid")
		}
		if len(result.Errors) == 0 {
			t.Error("expected at least one error")
		}
	})
}

func TestGenerateSlug(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Hello World", "hello-world"},
		{"  Multiple   Spaces  ", "multiple-spaces"},
		{"Special!@#$%Characters", "special-characters"},
		{"Cafe au Lait", "cafe-au-lait"},
		{"123 Numbers Start", "123-numbers-start"},
		{"UPPERCASE", "uppercase"},
		{"under_scores", "under-scores"},
		{"---Multiple---Dashes---", "multiple-dashes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateSlug(tt.name)
			if got != tt.want {
				t.Errorf("generateSlug(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		input   string
		want    EntityStatus
		wantErr bool
	}{
		{"draft", StatusDraft, false},
		{"published", StatusPublished, false},
		{"archived", StatusArchived, false},
		{"invalid", "", true},
		{"", "", true},
		{"DRAFT", "", true}, // Case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseStatus(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStatus(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseStatus(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
