package server

import (
	"bytes"
	"io"
	"net/http"

	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/render"
)

// commentConfirmRenderer is the part of *render.Engine that draws the confirm
// and invalid-link pages.
type commentConfirmRenderer interface {
	RenderCommentConfirm(w io.Writer, v *render.PageView, status int) error
}

// writeCommentConfirm renders the themed confirm or invalid-link page. Without
// a themed engine (no render handlers, a test engine) or on a render error it
// falls back to identity's plain page. Either way the page is never stored,
// indexed or sent a referrer: the token is in the URL.
func (s *Server) writeCommentConfirm(w http.ResponseWriter, r *http.Request, status int,
	cv *render.CommentConfirmView, fallback identity.PageData) {
	h := s.renderHandlers
	if h != nil && h.engine != nil {
		if cr, ok := h.engine.(commentConfirmRenderer); ok {
			v := h.errorView(r, &routeResult{NotFound: true})
			v.CommentConfirm = cv
			v.Page.Title = "" // the layout sets its own title
			var buf bytes.Buffer
			if err := cr.RenderCommentConfirm(&buf, v, status); err == nil {
				hd := w.Header()
				hd.Set("Content-Type", "text/html; charset=utf-8")
				applyRenderSecurityHeaders(hd, h.csp(), cspExtras{})
				setCommentConfirmHeaders(hd)
				w.WriteHeader(status)
				if r.Method != http.MethodHead {
					_, _ = w.Write(buf.Bytes())
				}
				return
			} else if s.logger != nil {
				// Neither the token nor the path is logged.
				s.logger.ErrorContext(r.Context(), "render: comment confirm page", "error", err)
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	setCommentConfirmHeaders(w.Header())
	w.WriteHeader(status)
	_ = identity.RenderPage(w, fallback)
}

// setCommentConfirmHeaders keeps the link private: the token is in the URL.
func setCommentConfirmHeaders(hd http.Header) {
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Robots-Tag", "noindex")
	hd.Set("Referrer-Policy", "no-referrer")
}
