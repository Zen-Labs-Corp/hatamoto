package pluginhost

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/secretbox"
)

const (
	settingsKeyDomain = "hatamoto/plugin/settings"
	// SealedPrefix starts every sealed plugin setting value stored in
	// hatamoto_plugin_settings; the rest is base64 of the secretbox blob.
	SealedPrefix = "hm-sealed:v1:"

	unreadableWarnWindow = time.Hour
)

var errNoSettingsKey = errors.New("pluginhost: no server secret to seal write-only settings")

// SettingsKey derives the plugin-settings key from the server secret; nil when
// the secret is empty.
func SettingsKey(secret string) []byte {
	if secret == "" {
		return nil
	}
	return secretbox.DeriveKey(secret, settingsKeyDomain)
}

// settingsAAD binds a sealed value to its plugin and key, so a blob cannot be
// moved to another key or plugin. A NUL cannot occur in a Postgres text value.
func settingsAAD(plugin, key string) []byte {
	return []byte("plugin-settings:" + plugin + "\x00" + key)
}

// sealValue seals raw (the property's JSON) as a prefixed JSON string.
func sealValue(k []byte, plugin, name string, raw jsontext.Value) (jsontext.Value, error) {
	if len(k) == 0 {
		return nil, errNoSettingsKey
	}
	blob, err := secretbox.Seal(k, settingsAAD(plugin, name), raw)
	if err != nil {
		return nil, err
	}
	out, err := jsonx.MarshalPinned(SealedPrefix + base64.StdEncoding.EncodeToString(blob))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sealedText returns the string of a prefixed JSON string value.
func sealedText(raw jsontext.Value) (string, bool) {
	var s string
	if err := jsonx.Unmarshal(raw, &s, jsonx.StoredLoose); err != nil || !strings.HasPrefix(s, SealedPrefix) {
		return "", false
	}
	return s, true
}

// openValue opens a sealed value. sealed is false when raw is not a prefixed
// JSON string; sealed with ok false means it cannot be opened (no key, damaged
// text, or another key, plugin or property name).
func openValue(k []byte, plugin, name string, raw jsontext.Value) (plain jsontext.Value, sealed, ok bool) {
	text, sealed := sealedText(raw)
	if !sealed {
		return nil, false, false
	}
	if len(k) == 0 {
		return nil, true, false
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(text, SealedPrefix))
	if err != nil {
		return nil, true, false
	}
	p, err := secretbox.Open(k, settingsAAD(plugin, name), blob)
	if err != nil {
		return nil, true, false
	}
	return jsontext.Value(p), true, true
}

func isBlank(raw jsontext.Value) bool {
	s := string(bytes.TrimSpace(raw))
	return s == `""` || s == "null"
}

// sealWriteOnly seals the non-empty, non-null top-level writeOnly values of v.
// A value that is already sealed and opens under k is kept (no double seal). A
// non-writeOnly string that merely starts with the prefix and does not open is
// sealed too, so it round-trips. Nothing to seal returns v unchanged; with no
// key and something to seal it fails (never a plain write).
func sealWriteOnly(k []byte, plugin string, schema []byte, v jsontext.Value) (jsontext.Value, error) {
	props := parseProps(schema).Properties
	obj := parseObject(v)
	changed := false
	for name, raw := range obj {
		if p := props[name]; p.WriteOnly && isBlank(raw) {
			continue
		}
		_, sealed, ok := openValue(k, plugin, name, raw)
		if ok || (!sealed && !props[name].WriteOnly) {
			continue
		}
		s, err := sealValue(k, plugin, name, raw)
		if err != nil {
			return nil, err
		}
		obj[name], changed = s, true
	}
	if !changed {
		return v, nil
	}
	return mustMarshal(obj), nil
}

// openSealed opens every top-level prefixed string of v, whatever the schema
// says. Values that cannot be opened are omitted and named (sorted). Without a
// prefixed value v is returned byte for byte.
func openSealed(k []byte, plugin string, v jsontext.Value) (jsontext.Value, []string) {
	obj := parseObject(v)
	var bad []string
	touched := false
	for name, raw := range obj {
		plain, sealed, ok := openValue(k, plugin, name, raw)
		if !sealed {
			continue
		}
		touched = true
		if ok {
			obj[name] = plain
			continue
		}
		delete(obj, name)
		bad = append(bad, name)
	}
	if !touched {
		return v, nil
	}
	slices.Sort(bad)
	return mustMarshal(obj), bad
}

// sealLegacy is the boot step's transform of one row: it seals plain non-empty
// writeOnly values and skips prefixed ones (counting those that do not open as
// unreadable). Other properties are untouched. With nothing sealed, out is v.
func sealLegacy(k []byte, plugin string, schema []byte, v jsontext.Value) (out jsontext.Value, sealed, unreadable int, err error) {
	props := parseProps(schema).Properties
	obj := parseObject(v)
	for name, raw := range obj {
		if !props[name].WriteOnly || isBlank(raw) {
			continue
		}
		if _, isSealed, ok := openValue(k, plugin, name, raw); isSealed {
			if !ok {
				unreadable++
			}
			continue
		}
		s, err := sealValue(k, plugin, name, raw)
		if err != nil {
			return v, 0, 0, err
		}
		obj[name] = s
		sealed++
	}
	if sealed == 0 {
		return v, 0, unreadable, nil
	}
	return mustMarshal(obj), sealed, unreadable, nil
}

// unreadableWarner logs one WARN per (plugin, key) per window. The zero value
// is ready to use.
type unreadableWarner struct {
	mu     sync.Mutex
	last   map[string]time.Time
	window time.Duration
	now    func() time.Time
}

func (w *unreadableWarner) warn(log *slog.Logger, plugin string, keys []string) {
	if len(keys) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now, window := time.Now, w.window
	if w.now != nil {
		now = w.now
	}
	if window == 0 {
		window = unreadableWarnWindow
	}
	if w.last == nil {
		w.last = map[string]time.Time{}
	}
	t := now()
	for _, key := range keys {
		id := plugin + "\x00" + key
		if prev, ok := w.last[id]; ok && t.Sub(prev) < window {
			continue
		}
		w.last[id] = t
		log.Warn("plugin settings: sealed value cannot be read; the plugin sees it as unset",
			slog.String("plugin", plugin), slog.String("key", key))
	}
}
