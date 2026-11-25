package entity

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"sort"

	"hatamoto.dev/core/internal/jsonx"
)

// UpdatePreview is the answer of a dry-run update: what the update would store.
type UpdatePreview struct {
	DryRun        bool              `json:"dry_run"`
	Version       int               `json:"version"` // the version the changes were built from
	Changes       []AttributeChange `json:"changes"` // never nil
	Errors        []string          `json:"errors"`  // never nil
	PendingReview bool              `json:"pending_review,omitempty"`
	authorized    bool              // set once the review/authz check passed
}

// AttributeChange is one changed attribute: Path joins nested keys with ".".
// Before is nil when the key is added, After is nil when it is removed.
type AttributeChange struct {
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

func newUpdatePreview() *UpdatePreview {
	return &UpdatePreview{DryRun: true, Changes: []AttributeChange{}, Errors: []string{}}
}

// fill records the changes from before to after and whether the real write
// would be sent for review.
func (p *UpdatePreview) fill(before, after jsontext.Value, held bool) error {
	changes, err := diffAttributes(before, after)
	if err != nil {
		return err
	}
	p.Changes, p.PendingReview = changes, held
	return nil
}

// diffAttributes lists the leaf differences between two attribute documents,
// sorted by path. Objects are compared key by key; arrays and scalars are one
// change at their path. Numbers are compared by their exact text.
func diffAttributes(before, after jsontext.Value) ([]AttributeChange, error) {
	var a, b any
	if err := decodeAttrs(before, &a); err != nil {
		return nil, fmt.Errorf("diff before: %w", err)
	}
	if err := decodeAttrs(after, &b); err != nil {
		return nil, fmt.Errorf("diff after: %w", err)
	}
	changes := []AttributeChange{}
	walkDiff("", a, b, &changes)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

func decodeAttrs(raw jsontext.Value, v *any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		*v = map[string]any{}
		return nil
	}
	return jsonx.Unmarshal(raw, v, jsonx.ExactNumbersLoose)
}

func walkDiff(path string, a, b any, out *[]AttributeChange) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, AttributeChange{Path: path, Before: a, After: b})
		}
		return
	}
	for k, av := range am {
		walkDiff(diffPath(path, k), av, bm[k], out) // a missing key reads as nil: removed
	}
	for k, bv := range bm {
		if _, ok := am[k]; !ok {
			walkDiff(diffPath(path, k), nil, bv, out)
		}
	}
}

func diffPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
