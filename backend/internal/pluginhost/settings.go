// Package pluginhost holds the host-side glue between the core and the
// plugin SDK: settings storage, service adapters, manifest validation and
// extension bridges.
package pluginhost

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v5"

	"hatamoto.dev/core/internal/jsonx"
)

// FieldError is one schema violation, addressed by JSON pointer.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SettingsStore persists plugin settings in hatamoto_plugin_settings.
type SettingsStore struct{ pool *pgxpool.Pool }

// NewSettingsStore returns a store over pool.
func NewSettingsStore(pool *pgxpool.Pool) *SettingsStore { return &SettingsStore{pool: pool} }

// CompileSchema compiles a plugin settings schema (JSON Schema 2020-12).
func CompileSchema(schema []byte) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource("settings.json", bytes.NewReader(schema)); err != nil {
		return nil, fmt.Errorf("pluginhost: parse settings schema: %w", err)
	}
	sch, err := c.Compile("settings.json")
	if err != nil {
		return nil, fmt.Errorf("pluginhost: compile settings schema: %w", err)
	}
	return sch, nil
}

// Get returns the stored settings object, or {} when none were saved.
func (s *SettingsStore) Get(ctx context.Context, plugin string) (jsontext.Value, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT settings FROM hatamoto_plugin_settings WHERE plugin = $1`, plugin).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return jsontext.Value(`{}`), nil
	}
	if err != nil {
		return nil, fmt.Errorf("pluginhost: load settings for %q: %w", plugin, err)
	}
	return jsontext.Value(raw), nil
}

// Put upserts the full settings object.
func (s *SettingsStore) Put(ctx context.Context, plugin string, v jsontext.Value) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO hatamoto_plugin_settings (plugin, settings, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (plugin) DO UPDATE SET settings = EXCLUDED.settings, updated_at = now()`,
		plugin, []byte(v))
	if err != nil {
		return fmt.Errorf("pluginhost: save settings for %q: %w", plugin, err)
	}
	return nil
}

// Validate checks v against sch and returns the violations (nil when valid).
func Validate(sch *jsonschema.Schema, v jsontext.Value) []FieldError {
	var doc any
	if err := jsonx.Unmarshal(v, &doc, jsonx.StoredLoose); err != nil {
		return []FieldError{{Path: "/", Message: "invalid JSON: " + err.Error()}}
	}
	err := sch.Validate(doc)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []FieldError{{Path: "/", Message: err.Error()}}
	}
	var out []FieldError
	collectLeaves(ve, &out)
	return out
}

func collectLeaves(ve *jsonschema.ValidationError, out *[]FieldError) {
	if len(ve.Causes) == 0 {
		path := ve.InstanceLocation
		if path == "" {
			path = "/"
		}
		*out = append(*out, FieldError{Path: path, Message: ve.Message})
		return
	}
	for _, c := range ve.Causes {
		collectLeaves(c, out)
	}
}

type schemaProps struct {
	Properties map[string]struct {
		Default   jsontext.Value `json:"default"`
		WriteOnly bool           `json:"writeOnly"`
	} `json:"properties"`
}

func parseProps(schema []byte) schemaProps {
	var p schemaProps
	_ = jsonx.Unmarshal(schema, &p, jsonx.StoredLoose) // an unparsable schema has no defaults/masks
	return p
}

func parseObject(raw jsontext.Value) map[string]jsontext.Value {
	obj := map[string]jsontext.Value{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = jsonx.Unmarshal(raw, &obj, jsonx.StoredLoose)
	}
	if obj == nil {
		obj = map[string]jsontext.Value{}
	}
	return obj
}

func mustMarshal(obj map[string]jsontext.Value) jsontext.Value {
	b, _ := jsonx.MarshalPinned(obj) // map of raw values cannot fail
	return b
}

// MergeDefaults fills top-level properties missing from stored with the
// schema's `default` values.
func MergeDefaults(schema []byte, stored jsontext.Value) jsontext.Value {
	obj := parseObject(stored)
	for name, p := range parseProps(schema).Properties {
		if _, ok := obj[name]; !ok && len(p.Default) > 0 {
			obj[name] = p.Default
		}
	}
	return mustMarshal(obj)
}

// MaskWriteOnly blanks top-level `writeOnly` properties so secrets are never
// returned by the API.
func MaskWriteOnly(schema []byte, v jsontext.Value) jsontext.Value {
	obj := parseObject(v)
	for name, p := range parseProps(schema).Properties {
		if _, ok := obj[name]; ok && p.WriteOnly {
			obj[name] = jsontext.Value(`""`)
		}
	}
	return mustMarshal(obj)
}

// WriteOnlySet names the top-level `writeOnly` properties that hold a
// non-empty stored value, so a form can say "stored" without showing it.
func WriteOnlySet(schema []byte, stored jsontext.Value) map[string]bool {
	obj := parseObject(stored)
	set := map[string]bool{}
	for name, p := range parseProps(schema).Properties {
		v, ok := obj[name]
		if s := string(bytes.TrimSpace(v)); ok && p.WriteOnly && s != `""` && s != "null" {
			set[name] = true
		}
	}
	return set
}

// PreserveWriteOnly keeps the stored value of a top-level `writeOnly`
// property when the incoming object omits it or sends an empty string. An
// explicit null clears the stored value.
func PreserveWriteOnly(schema []byte, old, in jsontext.Value) jsontext.Value {
	oldObj, inObj := parseObject(old), parseObject(in)
	for name, p := range parseProps(schema).Properties {
		if !p.WriteOnly {
			continue
		}
		cur, present := inObj[name]
		if present && string(bytes.TrimSpace(cur)) == "null" {
			delete(inObj, name)
			continue
		}
		if present && string(bytes.TrimSpace(cur)) != `""` {
			continue
		}
		if prev, ok := oldObj[name]; ok {
			inObj[name] = prev
		} else if present {
			delete(inObj, name)
		}
	}
	return mustMarshal(inObj)
}
