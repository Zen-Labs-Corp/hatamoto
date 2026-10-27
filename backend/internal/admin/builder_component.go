package admin

import (
	"encoding/json"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/builder"
)

// componentPrefix is the form-field prefix of the content of component node id.
// The outline form and the inspector's endpoint post under the same prefix.
func componentPrefix(id string) string { return "n." + id + ".c." }

// decodeComponent is the one decoder of component content: the no-JS outline
// and the inspector endpoint both read a node's fields through it. prev is the
// current content, so keys the theme schema does not list survive. Content is
// validated unless the post was a structural button (add/move/remove a row),
// which only reshapes the form. Sanitising stays with the save and render paths.
func (a *Admin) decodeComponent(def ThemeSection, form url.Values, prefix string, prev map[string]any) (content map[string]any, errs schemaform.Errors, structural bool) {
	structural = schemaform.Structural(form)
	content, errs = schemaform.Decode(def.Schema, form, cloneJSON(prev), schemaform.Options{Prefix: prefix})
	if content == nil {
		content = map[string]any{}
	}
	if structural {
		return content, nil, true
	}
	if raw, err := json.Marshal(content); err == nil {
		if verrs := schemaform.Validate(def.Schema, raw); len(verrs) > 0 {
			if errs == nil {
				errs = schemaform.Errors{}
			}
			for path, msgs := range verrs {
				if _, dup := errs[path]; !dup {
					errs[path] = msgs
				}
			}
		}
	}
	if len(errs) == 0 {
		errs = nil
	}
	return content, errs, false
}

// renderComponentForm renders the fields of a component's content under prefix.
func (a *Admin) renderComponentForm(def ThemeSection, content map[string]any, prefix string, errs schemaform.Errors) (template.HTML, error) {
	return schemaform.Render(def.Schema, content, schemaform.Options{Prefix: prefix, Errors: errs})
}

// componentReply is what the component endpoint answers.
type componentReply struct {
	HTML       string              `json:"html,omitempty"`
	Content    *map[string]any     `json:"content,omitempty"`
	Errors     map[string][]string `json:"errors,omitempty"`
	Structural bool                `json:"structural,omitempty"`
}

// handleBuilderComponent renders a component node's form (mode=form) or decodes
// the posted fields into content (mode=decode). Nothing is stored.
func (a *Admin) handleBuilderComponent(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.builderGate(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, previewMaxBody)
	var err error
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		err = r.ParseMultipartForm(1 << 20)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeBuilderJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "The form is too large."})
			return
		}
		writeBuilderJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body."})
		return
	}
	byType, _ := a.themeTypes()
	def, known := byType[r.PostFormValue("type")]
	if !known {
		writeBuilderJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "This section type is not in the theme.", "code": "unknown_type"})
		return
	}
	node := r.PostFormValue("node")
	if !builder.ValidID(node) {
		writeBuilderJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Invalid node id.", "code": "invalid_node"})
		return
	}
	prev := map[string]any{}
	if raw := r.PostFormValue("prev"); raw != "" {
		if json.Unmarshal([]byte(raw), &prev) != nil || prev == nil {
			writeBuilderJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "The current content is not a JSON object.", "code": "invalid_content"})
			return
		}
	}
	prefix := componentPrefix(node)

	var reply componentReply
	switch r.PostFormValue("mode") {
	case "form":
		html, err := a.renderComponentForm(def, prev, prefix, nil)
		if err != nil {
			a.serverError(w, r, "The section form could not be built", err)
			return
		}
		reply.HTML = string(html)
	case "decode":
		content, errs, structural := a.decodeComponent(def, r.PostForm, prefix, prev)
		reply.Content, reply.Errors, reply.Structural = &content, errs, structural
		if len(errs) > 0 || structural {
			html, err := a.renderComponentForm(def, content, prefix, errs)
			if err != nil {
				a.serverError(w, r, "The section form could not be built", err)
				return
			}
			reply.HTML = string(html)
		}
	default:
		writeBuilderJSON(w, http.StatusBadRequest, map[string]string{"error": "Unknown mode."})
		return
	}
	writeBuilderJSON(w, http.StatusOK, reply)
}
