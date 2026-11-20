package email

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

const (
	// maxModerationItems is how many held comments one moderator mail lists.
	maxModerationItems = 20
	// maxNotifyName caps a commenter-written name, in runes.
	maxNotifyName = 80
)

// CommentModerationItem is one held comment in a moderator mail.
type CommentModerationItem struct {
	AuthorName  string
	EntityTitle string
	Held        string // "contains a link, first comment"
}

// CommentModerationData drives comment_moderation.html (one comment or a digest).
// It carries no comment text: only the commenter's name, which stays out of every
// header.
type CommentModerationData struct {
	SiteName       string
	Items          []CommentModerationItem // at most 20 shown
	More           int                     // held comments not listed
	ModerateURL    string                  // <base><admin>/comments?status=pending
	UnsubscribeURL string
}

// CommentReplyData drives comment_reply.html. No reply text: only the replier's name.
type CommentReplyData struct {
	SiteName       string
	EntityTitle    string
	ReplierName    string
	URL            string
	UnsubscribeURL string
}

// SendCommentModeration mails a moderator about held comments: one comment is a
// KindCommentModeration mail, several are one KindCommentDigest mail.
func (s *Service) SendCommentModeration(ctx context.Context, to string, d CommentModerationData) error {
	if len(d.Items) == 0 {
		return fmt.Errorf("email: no held comments to report")
	}
	if len(d.Items) > maxModerationItems {
		d.More += len(d.Items) - maxModerationItems
		d.Items = d.Items[:maxModerationItems]
	}
	site := d.SiteName
	d.Items = append([]CommentModerationItem(nil), d.Items...)
	for i := range d.Items {
		d.Items[i].AuthorName = notifyName(d.Items[i].AuthorName)
		d.Items[i].EntityTitle = notifyText(d.Items[i].EntityTitle)
		d.Items[i].Held = notifyText(d.Items[i].Held)
	}
	d.SiteName = notifyText(d.SiteName)

	kind := KindCommentModeration
	subject := headerOr("New comment waiting on "+site, "New comment waiting")
	if total := len(d.Items) + d.More; total > 1 {
		kind = KindCommentDigest
		subject = headerOr(fmt.Sprintf("%d comments waiting on %s", total, site), "Comments waiting")
	}

	html, err := s.render("comment_moderation.html", d)
	if err != nil {
		return err
	}
	var b strings.Builder
	if kind == KindCommentModeration {
		it := d.Items[0]
		fmt.Fprintf(&b, "%s on \"%s\": held because: %s\n", it.AuthorName, it.EntityTitle, it.Held)
	} else {
		fmt.Fprintf(&b, "%d comments are waiting on %s:\n\n", len(d.Items)+d.More, d.SiteName)
		for _, it := range d.Items {
			fmt.Fprintf(&b, "- %s on \"%s\": held because: %s\n", it.AuthorName, it.EntityTitle, it.Held)
		}
		if d.More > 0 {
			fmt.Fprintf(&b, "and %d more\n", d.More)
		}
	}
	fmt.Fprintf(&b, "Moderate: %s\n\nStop these emails: %s\n", d.ModerateURL, d.UnsubscribeURL)
	return s.sendComment(ctx, kind, to, site, subject, html, b.String(), d.UnsubscribeURL)
}

// SendCommentReply mails a commenter that someone replied to their comment.
func (s *Service) SendCommentReply(ctx context.Context, to string, d CommentReplyData) error {
	site, title := d.SiteName, d.EntityTitle
	d.SiteName = notifyText(d.SiteName)
	d.EntityTitle = notifyText(d.EntityTitle)
	d.ReplierName = notifyName(d.ReplierName)
	html, err := s.render("comment_reply.html", d)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s replied to your comment on \"%s\".\nRead it: %s\n\nStop reply emails: %s\n",
		d.ReplierName, d.EntityTitle, d.URL, d.UnsubscribeURL)
	subject := headerOr(fmt.Sprintf("New reply on \"%s\"", title), "New reply to your comment")
	return s.sendComment(ctx, KindCommentReply, to, site, subject, html, text, d.UnsubscribeURL)
}

// sendComment sends one comment notification with its one-click unsubscribe header.
func (s *Service) sendComment(ctx context.Context, kind Kind, to, siteName, subject, html, text, unsub string) error {
	addr, err := commentRecipient(to)
	if err != nil {
		return err
	}
	opts := SendOptions{Kind: kind, ListUnsubscribe: unsub}
	if s.cfg.Load().FromNameFromSite {
		if clean, ok := SanitizeHeader(siteName); ok {
			opts.FromName = clean
		}
	}
	_, err = s.SendTrackedWith(ctx, opts, []string{addr}, subject, html, text)
	return err
}

// commentRecipient accepts only a bare address: no display name, no CR/LF.
func commentRecipient(to string) (string, error) {
	if strings.ContainsAny(to, "\r\n") {
		return "", ErrBadHeader
	}
	a, err := mail.ParseAddress(to)
	if err != nil || a.Name != "" || a.Address != to {
		return "", ErrBadHeader
	}
	return a.Address, nil
}

// headerOr returns subject when it is safe in a header, else the fixed fallback.
func headerOr(subject, fallback string) string {
	if clean, ok := SanitizeHeader(subject); ok {
		return clean
	}
	return fallback
}

// notifyText turns control characters into spaces.
func notifyText(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
}

// notifyName cleans a commenter-written name: control characters become
// spaces, at most 80 runes, "Someone" when nothing is left.
func notifyName(v string) string {
	v = strings.TrimSpace(notifyText(v))
	if r := []rune(v); len(r) > maxNotifyName {
		v = strings.TrimSpace(string(r[:maxNotifyName]))
	}
	if v == "" {
		return "Someone"
	}
	return v
}
