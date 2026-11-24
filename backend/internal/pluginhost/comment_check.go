package pluginhost

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const (
	commentCheckMaxReasons = 8
	commentCheckMaxReason  = 200 // bytes, including the "<plugin>: " prefix
)

// commentCheckFilter wraps a plugin's comment.check filter: the plugin sees the
// payload with Score 0 and no Reasons, and only its Score (cut to
// 0..sdk.CommentCheckMaxScore) and Reasons (at most 8, 200 bytes, prefixed with
// the plugin name) are added to the chain value. Only a plugin holding client.key
// is sent addr_key and area_key, on its own copy. Every other field it returns is
// ignored, so no plugin can change what the next one sees. Any error is returned
// as is: the chain runs under hooks.Engine.ApplyClosed, which holds the comment.
func (r *Runtime) commentCheckFilter(m sdk.Manifest, generic func(context.Context, any) (any, error)) func(context.Context, any) (any, error) {
	return func(ctx context.Context, v any) (any, error) {
		in, ok := v.(sdk.CommentCheck)
		if !ok {
			return v, fmt.Errorf("comment.check: payload %T", v)
		}
		send := in
		send.Score, send.Reasons = 0, nil
		send.AddrKey, send.AreaKey = r.clientKeys(ctx, m.Name)
		send.CommenterKey = r.commenterKeyFor(ctx, m.Name) // this plugin's copy only; never copied back
		res, err := generic(ctx, send)
		if err != nil {
			return v, err
		}
		got, ok := res.(sdk.CommentCheck)
		if !ok {
			return v, fmt.Errorf("comment.check: %s returned %T", m.Name, res)
		}
		in.Score += min(max(got.Score, 0), sdk.CommentCheckMaxScore)
		in.Reasons = append(in.Reasons, capCommentReasons(m.Name, got.Reasons)...)
		return in, nil
	}
}

// capCommentReasons drops empty reasons, prefixes "<plugin>: ", cuts each to
// commentCheckMaxReason bytes on a rune boundary and keeps the first
// commentCheckMaxReasons.
func capCommentReasons(plugin string, rs []string) []string {
	var out []string
	for _, r := range rs {
		r = strings.ToValidUTF8(r, "")
		if r == "" {
			continue
		}
		if len(out) == commentCheckMaxReasons {
			break
		}
		r = plugin + ": " + r
		if len(r) > commentCheckMaxReason {
			r = r[:commentCheckMaxReason]
			for !utf8.ValidString(r) {
				r = r[:len(r)-1]
			}
		}
		out = append(out, r)
	}
	return out
}
