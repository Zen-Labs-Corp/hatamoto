package entity

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"hatamoto.dev/core/internal/jsonx"
)

// MergePatch applies patch to target as a JSON merge patch (RFC 7396):
// object members merge recursively, a null member removes the key, and any
// other value (arrays included) replaces the target's value whole. A patch
// that is not an object replaces the whole target. A target that is empty,
// null or valid JSON but not an object counts as {}; one that does not parse
// is an error wrapping ErrStoredAttributes, so stored data is never dropped
// silently. Numbers keep their exact digits.
// ErrStoredAttributes marks a merge target that is not valid JSON.
var ErrStoredAttributes = errors.New("stored attributes are not valid JSON")

func MergePatch(target, patch jsontext.Value) (jsontext.Value, error) {
	var p any
	if err := jsonx.Unmarshal(patch, &p, jsonx.ExactNumbers); err != nil {
		return nil, err
	}
	pm, ok := p.(map[string]any)
	if !ok {
		return patch.Clone(), nil
	}
	var t any
	if len(bytes.TrimSpace(target)) > 0 {
		if err := jsonx.Unmarshal(target, &t, jsonx.ExactNumbersLoose); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStoredAttributes, err)
		}
	}
	tm, _ := t.(map[string]any)
	return jsonx.Marshal(mergePatchObject(tm, pm))
}

func mergePatchObject(t, p map[string]any) map[string]any {
	if t == nil {
		t = map[string]any{}
	}
	for k, v := range p {
		switch pv := v.(type) {
		case nil:
			delete(t, k)
		case map[string]any:
			tv, _ := t[k].(map[string]any)
			t[k] = mergePatchObject(tv, pv)
		default:
			t[k] = v
		}
	}
	return t
}
