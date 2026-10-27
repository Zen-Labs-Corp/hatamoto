package schemaform

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

var validator = entity.NewValidator()

var quoted = regexp.MustCompile(`'([^']+)'|"([^"]+)"`)

// Validate checks attrs against schema with the same validator the API uses
// and returns friendly messages keyed by dotted path. A schema or document
// failure that cannot be tied to a field lands under the "" key.
func Validate(schema, attrs json.RawMessage) Errors {
	res := validator.ValidateWithDetails(schema, attrs)
	if res.Valid {
		return nil
	}
	root, _ := ParseSchema(schema)
	errs := Errors{}
	add := func(path, msg string) {
		for _, m := range errs[path] {
			if m == msg {
				return
			}
		}
		errs[path] = append(errs[path], msg)
	}
	var generic []entity.ValidationError
	leafSeen := false
	for _, e := range res.Errors {
		if e.Keyword == "" {
			generic = append(generic, e)
			continue
		}
		leafSeen = true
		path := dotted(e.Path)
		if e.Keyword == "required" {
			for _, m := range quoted.FindAllStringSubmatch(e.Message, -1) {
				prop := m[1] + m[2]
				child := join(path, prop)
				add(child, friendly("required", root, child, ""))
			}
			continue
		}
		add(path, friendly(e.Keyword, root, path, e.Message))
	}
	if !leafSeen {
		for _, e := range generic {
			add(dotted(e.Path), capitalise(e.Message))
		}
	}
	return errs
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

// dotted turns a JSON pointer "/faqs/2/question" into "faqs.2.question".
func dotted(ptr string) string {
	ptr = strings.TrimPrefix(ptr, "/")
	if ptr == "" {
		return ""
	}
	parts := strings.Split(ptr, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return strings.Join(parts, ".")
}

// nodeAt finds the schema node of a dotted path (indices step into items).
func nodeAt(root *Node, path string) (*Node, string) {
	n, name := root, ""
	if path == "" {
		return n, name
	}
	for _, seg := range strings.Split(path, ".") {
		if n == nil {
			return nil, name
		}
		if _, err := strconv.Atoi(seg); err == nil {
			n = n.Items
			continue
		}
		n, name = n.prop(seg), seg
	}
	return n, name
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// friendly maps a failed keyword to the admin's wording.
func friendly(keyword string, root *Node, path, libMsg string) string {
	n, name := nodeAt(root, path)
	label := name
	if n != nil {
		label = n.label(name)
	}
	if label == "" {
		label = "This field"
	}
	num := func(f *float64) string { return strconv.FormatFloat(*f, 'f', -1, 64) }
	switch keyword {
	case "required":
		return label + " is required"
	case "minLength":
		if n != nil && n.MinLength != nil {
			return fmt.Sprintf("Use at least %d characters", *n.MinLength)
		}
	case "maxLength":
		if n != nil && n.MaxLength != nil {
			return fmt.Sprintf("Use at most %d characters", *n.MaxLength)
		}
	case "pattern":
		return label + " is not in the expected format"
	case "format":
		switch {
		case n != nil && n.Format == "email":
			return "Enter a valid email address"
		case n != nil && (n.Format == "uri" || n.Format == "url"):
			return "Enter a valid URL"
		case n != nil && strings.HasPrefix(n.Format, "date"):
			return "Enter a valid date"
		case n != nil && n.Format == "time":
			return "Enter a valid time"
		}
	case "minimum", "exclusiveMinimum":
		if n != nil && n.Minimum != nil {
			return "Enter a number of at least " + num(n.Minimum)
		}
	case "maximum", "exclusiveMaximum":
		if n != nil && n.Maximum != nil {
			return "Enter a number of at most " + num(n.Maximum)
		}
	case "enum":
		return "Choose one of the options"
	case "type":
		if n != nil {
			switch n.Type {
			case "integer":
				return "Enter a whole number"
			case "number":
				return "Enter a number"
			case "boolean":
				return "Choose yes or no"
			case "array":
				return "Enter a list"
			case "object":
				return "Enter an object"
			}
		}
		return "Enter a valid value"
	case "minItems":
		if n != nil && n.MinItems != nil {
			s := "s"
			if *n.MinItems == 1 {
				s = ""
			}
			return fmt.Sprintf("Add at least %d item%s", *n.MinItems, s)
		}
	case "maxItems":
		if n != nil && n.MaxItems != nil {
			return fmt.Sprintf("Add at most %d", *n.MaxItems)
		}
	}
	if libMsg == "" {
		return label + " is not valid"
	}
	return capitalise(libMsg)
}
