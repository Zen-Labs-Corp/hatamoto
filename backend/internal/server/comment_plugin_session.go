package server

import (
	"net/http"
	"time"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// pluginCommenterSession applies a plugin's commenter_session
// (pluginhost.Config.CommenterSession): it signs this browser in as the
// commenter the plugin vouches for, or out. The same-origin rule and the
// cookie's Secure attribute are the email-link sign-in's. The raw account is
// never logged or audited.
func (s *Server) pluginCommenterSession(w http.ResponseWriter, r *http.Request, plugin string, cs sdk.CommenterSession) error {
	if !commentConfirmSameOrigin(r) {
		return pluginhost.ErrSessionCrossOrigin
	}
	if s.commentIdentity == nil || s.comments == nil {
		return pluginhost.ErrSessionUnavailable
	}
	secure := s.commentCookieSecure(r)
	if cs.SignOut {
		http.SetCookie(w, s.commentIdentity.ClearCookie(secure))
		return nil
	}
	ctx := r.Context()
	target := "plugin:" + plugin
	if cs.Link {
		if cur, ok := s.commentDevice(r); ok {
			bound, err := s.comments.BindPluginCommenter(ctx, cur, plugin, cs.Account)
			if err != nil {
				return err
			}
			if bound {
				s.pluginModerationAudit(ctx, "plugin.comment.identity", target, map[string]any{"fn": "link", "commenter": cur.String()})
			}
		}
	}
	id, created, err := s.comments.EnsurePluginCommenter(ctx, plugin, cs.Account)
	if err != nil {
		return err
	}
	if created {
		s.pluginModerationAudit(ctx, "plugin.comment.identity", target, map[string]any{"fn": "ensure", "commenter": id.String()})
	}
	c, err := s.commentIdentity.CookieFor(ctx, id, secure, time.Duration(cs.MaxAge)*time.Second)
	if err != nil {
		return err
	}
	http.SetCookie(w, c)
	return nil
}
