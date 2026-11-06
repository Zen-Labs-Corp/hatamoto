package form

import (
	"strconv"
	"strings"
)

// Logic operators of a FieldLogic.
const (
	LogicIs       = "is"
	LogicIsNot    = "is_not"
	LogicContains = "contains"
	LogicFilled   = "filled"
	LogicEmpty    = "empty"
)

func validLogicOp(op string) bool {
	switch op {
	case LogicIs, LogicIsNot, LogicContains, LogicFilled, LogicEmpty:
		return true
	}
	return false
}

// Visible reports, for every field that has an id, whether the field is shown
// given the answers in data (keyed by field name). A field whose logic source
// is itself hidden sees an empty source. A logic cycle counts as hidden.
func Visible(fields []FormField, data map[string]any) map[string]bool {
	byID := make(map[string]*FormField, len(fields))
	for i := range fields {
		if fields[i].ID != "" {
			byID[fields[i].ID] = &fields[i]
		}
	}
	const (
		running = 1
		shown   = 2
		hidden  = 3
	)
	state := make(map[string]int, len(byID))
	var visible func(id string) bool
	visible = func(id string) bool {
		switch state[id] {
		case running, hidden:
			return false
		case shown:
			return true
		}
		f := byID[id]
		state[id] = running
		show := true
		if f.Logic != nil {
			var src any
			srcShown := false
			if s := byID[f.Logic.FieldID]; s != nil {
				srcShown = visible(s.ID)
				if srcShown {
					src = data[s.Name]
				}
			}
			show = evalLogic(f.Logic, src)
		}
		if show {
			state[id] = shown
		} else {
			state[id] = hidden
		}
		return show
	}
	out := make(map[string]bool, len(byID))
	for id := range byID {
		out[id] = visible(id)
	}
	return out
}

// evalLogic reports whether the condition holds for the source answer.
func evalLogic(l *FieldLogic, src any) bool {
	switch l.Op {
	case LogicFilled:
		return !emptyForLogic(src)
	case LogicEmpty:
		return emptyForLogic(src)
	case LogicIs:
		return answerHas(src, l.Value, false)
	case LogicIsNot:
		return !answerHas(src, l.Value, false)
	case LogicContains:
		return answerHas(src, l.Value, true)
	}
	return false
}

func emptyForLogic(v any) bool {
	if b, ok := v.(bool); ok {
		return !b
	}
	return isEmptyAnswer(v)
}

// answerHas matches a list answer by member and a text answer by equality, or
// by substring when sub is set.
func answerHas(v any, want string, sub bool) bool {
	if list, ok := v.([]any); ok {
		for _, it := range list {
			if logicText(it) == want {
				return true
			}
		}
		return false
	}
	s := logicText(v)
	if sub {
		return strings.Contains(s, want)
	}
	return s == want
}

func logicText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
