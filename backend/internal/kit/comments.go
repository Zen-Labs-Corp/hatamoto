package kit

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"math"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/jsonx"
)

// kitComments is the comment order and policy a kit schema asks for. They are
// written only through Deps.SchemaComments; the rest of settings is written as is.
type kitComments struct {
	Order               string
	HasOrder, HasPolicy bool
	Policy              comment.Policy // Enabled always false: only the policy switch turns it on
}

// splitComments removes settings.comments.order and .policy from raw and returns
// them. A missing policy number is comment.DefaultPolicy()'s; a non-integer is an
// error. policy.enabled is ignored. An emptied "comments" object is dropped.
func splitComments(raw jsontext.Value) (rest jsontext.Value, c kitComments, err error) {
	if len(raw) == 0 || raw.Kind() != '{' {
		return raw, c, nil
	}
	var m map[string]any
	if err := jsonx.Unmarshal(raw, &m); err != nil {
		return nil, c, err
	}
	cm, ok := m["comments"].(map[string]any)
	if !ok {
		return raw, c, nil
	}
	order, hasOrder := cm["order"]
	pol, hasPolicy := cm["policy"]
	if !hasOrder && !hasPolicy {
		return raw, c, nil
	}
	if hasOrder {
		s, ok := order.(string)
		if !ok {
			return nil, c, errors.New("order must be text")
		}
		c.Order, c.HasOrder = s, s != ""
		delete(cm, "order")
	}
	if hasPolicy {
		pm, ok := pol.(map[string]any)
		if !ok {
			return nil, c, errors.New("policy must be an object")
		}
		c.HasPolicy = true
		c.Policy = comment.DefaultPolicy()
		for key, dst := range map[string]*int{
			"max_depth":           &c.Policy.MaxDepth,
			"edit_window_minutes": &c.Policy.EditWindowMinutes,
			"slow_mode_seconds":   &c.Policy.SlowModeSeconds,
		} {
			v, ok := pm[key]
			if !ok {
				continue
			}
			f, isNum := v.(float64)
			if !isNum || f != math.Trunc(f) || math.Abs(f) > math.MaxInt32 {
				return nil, c, fmt.Errorf("policy.%s must be a whole number", key)
			}
			*dst = int(f)
		}
		delete(cm, "policy")
	}
	if len(cm) == 0 {
		delete(m, "comments")
	}
	b, err := jsonx.Marshal(m)
	return jsontext.Value(b), c, err
}

// check runs the seam's own rules (comment.Policy.Validate, comment.ValidOrder).
func (c kitComments) check() error {
	if !comment.ValidOrder(c.Order) {
		return fmt.Errorf("order %q is not a known order", c.Order)
	}
	if !c.HasPolicy {
		return nil
	}
	if err := c.Policy.Validate(); err != nil {
		var fe *comment.FieldError
		if errors.As(err, &fe) {
			return fmt.Errorf("%s %s", fe.Field, fe.Message)
		}
		return err
	}
	return nil
}

// policy is what the seam is asked to store for the kit's schema.
func (c kitComments) policy() comment.Policy {
	if c.HasPolicy {
		return c.Policy
	}
	return comment.DefaultPolicy()
}
