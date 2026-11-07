package server

import (
	"cmp"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/middleware"
)

// maxFormPostBodySize caps urlencoded form posts (1 MiB, same as JSON bodies).
const maxFormPostBodySize = 1 << 20

// handlePublicFormPost is the JS-off submit path for server-rendered forms:
// it accepts application/x-www-form-urlencoded (or multipart/form-data for
// forms with a file field), stores the submission through
// the same code as the JSON endpoint and answers with a 303 back to the page.
// Success appends ?submitted=1, any failure ?form_error=1.
func (s *Server) handlePublicFormPost(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "form slug is required")
		return
	}

	// A form with a file input posts multipart; the files are staged and
	// checked like on the JSON endpoint. Any failure is the generic error.
	var (
		formObj *form.Form
		post    url.Values
		files   uploads
		err     error
	)
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
		if formObj, err = s.activeFormBySlug(r.Context(), slug); err != nil {
			http.Redirect(w, r, formPostRedirect("/", "form_error"), http.StatusSeeOther)
			return
		}
		post, files, err = s.readMultipartSubmit(w, r, formObj)
		if err != nil {
			http.Redirect(w, r, formPostRedirect(safeLocalPath(post.Get("_redirect")), "form_error"), http.StatusSeeOther)
			return
		}
		defer files.discard()
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormPostBodySize)
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, formPostRedirect("/", "form_error"), http.StatusSeeOther)
			return
		}
		post = r.PostForm
	}
	back := safeLocalPath(post.Get("_redirect"))
	// A sandboxed page needs a valid timing token; without one nothing is
	// stored and the visitor gets the same answer as any rejected submission.
	if originNull(r) && !s.timingTokenOK(slug, controlFromValues(post).Timing) {
		http.Redirect(w, r, formPostRedirect(back, "form_error"), http.StatusSeeOther)
		return
	}

	if formObj == nil {
		if formObj, err = s.activeFormBySlug(r.Context(), slug); err != nil {
			http.Redirect(w, r, formPostRedirect(back, "form_error"), http.StatusSeeOther)
			return
		}
	}

	// Honeypot: bots fill the hidden field. Answer as if it worked, store
	// nothing. A form can switch the trap off.
	if !formObj.Settings.HoneypotOff && post.Get(honeypotField) != "" {
		http.Redirect(w, r, formPostRedirect(back, "submitted"), http.StatusSeeOther)
		return
	}

	data := formValues(post)

	ctl := controlFromValues(post)
	_, err = s.storeFormSubmission(r.Context(), formObj, slug, data, files, ctl, submitMeta{
		IPAddress: middleware.GetClientIP(r),
		TrustedIP: middleware.GetClientIP(r),
		UserAgent: r.UserAgent(),
		Referrer:  r.Referer(),
		PageURL:   cmp.Or(ctl.Page, post.Get("page_url")),
		Host:      r.Host,
	})
	if err != nil {
		http.Redirect(w, r, formPostRedirect(back, "form_error"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, formPostRedirect(back, "submitted"), http.StatusSeeOther)
}

// formValues turns posted values into submission data: a single value stays a
// string, several become a list. The handler's own control fields are dropped.
func formValues(post url.Values) map[string]interface{} {
	data := make(map[string]interface{}, len(post))
	for k, vals := range post {
		switch {
		case k == "_redirect" || k == honeypotField || k == "page_url" || isControlField(k) || len(vals) == 0:
		case len(vals) == 1:
			data[k] = vals[0]
		default:
			multi := make([]interface{}, len(vals))
			for i, v := range vals {
				multi[i] = v
			}
			data[k] = multi
		}
	}
	return data
}

// parseFormValues reads a urlencoded body for the JSON endpoint.
func parseFormValues(w http.ResponseWriter, r *http.Request) (map[string]interface{}, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormPostBodySize)
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return formValues(r.PostForm), nil
}

// safeLocalPath returns p when it is a same-site absolute path ("/x?y"),
// otherwise "/". Scheme-relative ("//host"), backslash and control-character
// forms that browsers would treat as another origin are rejected.
func safeLocalPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return u.EscapedPath() + queryOf(u)
}

func queryOf(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

// formPostRedirect appends flag=1 to a local path, replacing earlier outcome flags.
func formPostRedirect(local, flag string) string {
	u, err := url.Parse(local)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Del("submitted")
	q.Del("form_error")
	q.Set(flag, "1")
	u.RawQuery = q.Encode()
	return u.String()
}
