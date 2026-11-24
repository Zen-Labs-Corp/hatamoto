package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// commentDraftMaxName is the longest author name a comment.draft filter may give.
const commentDraftMaxName = 80

// commentDraftFilter wraps a plugin's comment.draft filter. The plugin sees the
// commenter's key and the schema slug (author_name and reject arrive empty);
// only its author_name or its reject message is taken from the answer, every
// other field is ignored, so no plugin can change what the next one sees.
//
// A non-empty reject comes back as *sdk.Reject carrying the cleaned public
// message. Every failure (no commenter, a guest Reject error, a trap, a
// timeout, an empty or over-long name) is an error that is deliberately not
// matchable as *sdk.Reject, so the caller can tell a public refusal from a
// failure and show generic text for the latter.
func (r *Runtime) commentDraftFilter(m sdk.Manifest, generic func(context.Context, any) (any, error)) func(context.Context, any) (any, error) {
	return func(ctx context.Context, v any) (any, error) {
		in, ok := v.(sdk.CommentDraft)
		if !ok {
			return v, fmt.Errorf("comment.draft: payload %T", v)
		}
		send := sdk.CommentDraft{Schema: in.Schema}
		send.CommenterKey = r.commenterKeyFor(ctx, m.Name)
		if send.CommenterKey == "" {
			return v, errors.New("comment.draft: no commenter")
		}
		out, err := generic(ctx, send)
		if err != nil {
			// %v, not %w: a *sdk.Reject from the plugin's own error is a
			// failure here; only the reject field is shown to the visitor.
			return v, fmt.Errorf("comment.draft: %s: %v", m.Name, err)
		}
		res, ok := out.(sdk.CommentDraft)
		if !ok {
			return v, fmt.Errorf("comment.draft: %s returned %T", m.Name, out)
		}
		if msg := cleanDraftReject(res.Reject); msg != "" {
			return v, &sdk.Reject{Reason: msg}
		}
		name := cleanDraftName(res.AuthorName)
		if name == "" {
			return v, fmt.Errorf("comment.draft: %s returned no usable name", m.Name)
		}
		in.AuthorName, in.CommenterKey, in.Reject = name, "", ""
		return in, nil
	}
}

// cleanDraftReject makes a plugin's reject text fit to show: valid UTF-8, no
// control characters, trimmed, at most sdk.CommentDraftMaxReject bytes.
func cleanDraftReject(s string) string {
	s = strings.TrimSpace(dropControl(strings.ToValidUTF8(s, "")))
	if len(s) > sdk.CommentDraftMaxReject {
		s = s[:sdk.CommentDraftMaxReject]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// cleanDraftName returns the trimmed name without control characters, or ""
// when it is empty or longer than commentDraftMaxName characters.
func cleanDraftName(s string) string {
	s = strings.TrimSpace(dropControl(strings.ToValidUTF8(s, "")))
	if n := utf8.RuneCountInString(s); n < 1 || n > commentDraftMaxName {
		return ""
	}
	return s
}

func dropControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
