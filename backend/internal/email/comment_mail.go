package email

import (
	"context"
	"fmt"
)

// CommentConfirmationData drives comment_confirmation.html. It deliberately has
// no field for the commenter's name or comment text: the site must never mail
// visitor-written text to an address nobody has confirmed yet.
type CommentConfirmationData struct {
	SiteName    string
	EntityTitle string
	URL         string // absolute, configured base only
	ExpiresIn   string // "24 hours"
}

// SendCommentConfirmation mails the single-use link that confirms a comment.
func (s *Service) SendCommentConfirmation(ctx context.Context, to string, data CommentConfirmationData) error {
	html, err := s.render("comment_confirmation.html", data)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Someone used this address to comment on \"%s\" at %s.\nConfirm it was you:\n\n  %s\n\nThe link works once and expires in %s.\n\nIf this was not you, ignore this email. Nothing will be published.\n",
		data.EntityTitle, data.SiteName, data.URL, data.ExpiresIn)
	subject := "Confirm your comment on " + data.SiteName
	if clean, ok := SanitizeHeader(subject); ok {
		subject = clean
	} else {
		subject = "Confirm your comment"
	}
	return s.sendAuth(ctx, KindCommentConfirmation, to, data.SiteName, subject, html, text)
}

// CommentSignInData drives comment_signin.html. Like the confirmation it has no
// field for visitor-written text.
type CommentSignInData struct {
	SiteName  string
	URL       string // absolute, configured base only
	ExpiresIn string // "24 hours"
}

// SendCommentSignIn mails the single-use link that signs a browser in as the
// commenter who owns the address. It is logged under KindCommentConfirmation.
func (s *Service) SendCommentSignIn(ctx context.Context, to string, data CommentSignInData) error {
	html, err := s.render("comment_signin.html", data)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Someone asked to sign in to comment at %s with this address.\nSign in:\n\n  %s\n\nThe link works once and expires in %s.\n\nIf this was not you, ignore this email. Nothing will happen.\n",
		data.SiteName, data.URL, data.ExpiresIn)
	subject := "Sign in to " + data.SiteName
	if clean, ok := SanitizeHeader(subject); ok {
		subject = clean
	} else {
		subject = "Sign in to comment"
	}
	return s.sendAuth(ctx, KindCommentConfirmation, to, data.SiteName, subject, html, text)
}
