package wpimport

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
)

// KindComment is the id-map kind of imported comments (WordPress comment id ->
// Hatamoto comment id); it makes a re-run skip what is already there.
const KindComment = "comment"

// maxPingList caps the pingbacks and trackbacks named in the result.
const maxPingList = 50

// maxThreadDepth stops orderThread on a parent cycle.
const maxThreadDepth = 64

// CommentsResult is what the comments step did. It holds counts and post
// titles only: never an email, an IP address or a URL.
type CommentsResult struct {
	Imported          int `json:"imported"`
	Approved          int `json:"approved"`
	Pending           int `json:"pending"`
	Spam              int `json:"spam"`
	Trash             int `json:"trash"`
	CommentersCreated int `json:"commenters_created"`
	CommentersUpdated int `json:"commenters_updated"`
	// Pings lists skipped pingbacks and trackbacks as "Post title (#123)"
	// (the WordPress comment id), capped; PingsTotal counts all of them.
	Pings            []string `json:"pings,omitempty"`
	PingsTotal       int      `json:"pings_total"`
	SkippedNoEmail   int      `json:"skipped_no_email,omitzero"`
	SkippedNoPost    int      `json:"skipped_no_post,omitzero"`
	SkippedOtherType int      `json:"skipped_other_type,omitzero"`
	SkippedBody      int      `json:"skipped_body,omitzero"`
	AlreadyImported  int      `json:"already_imported,omitzero"`
	Reparented       int      `json:"reparented,omitzero"`
	Clamped          int      `json:"clamped,omitzero"`
	// URLsDropped counts comments whose author website address was dropped.
	URLsDropped int `json:"urls_dropped,omitzero"`
	// Enabled is the site comment switch after the run; the import never changes it.
	Enabled bool `json:"enabled"`
}

// importContentAndComments imports the items, then their comments.
func (r *Runner) importContentAndComments(ctx context.Context) error {
	if err := r.importContent(ctx); err != nil {
		return err
	}
	return r.importComments(ctx)
}

// mapWPStatus maps a WordPress comment_approved value; known is false for a
// value that is mapped to pending only because it is not recognised.
func mapWPStatus(s string) (st comment.Status, known bool) {
	switch strings.TrimSpace(s) {
	case "1":
		return comment.StatusApproved, true
	case "0":
		return comment.StatusPending, true
	case "spam":
		return comment.StatusSpam, true
	case "trash", "post-trashed":
		return comment.StatusTrash, true
	}
	return comment.StatusPending, false
}

// isPlainComment reports whether a WordPress comment type is a real comment.
func isPlainComment(typ string) bool { return typ == "" || typ == "comment" }

// usableEmail reports whether an address can identify a commenter.
func usableEmail(email string) bool {
	e := comment.NormalizeEmail(email)
	return e != "" && strings.Contains(e, "@")
}

func topLevel(parent string) bool { return parent == "" || parent == "0" }

// orderThread returns cs with every parent before its children, keeping the
// input order otherwise. A parent that is not in cs, or that sits in a cycle,
// leaves the comment where it is (the importer then makes it top level).
func orderThread(cs []Comment) []Comment {
	idx := make(map[string]int, len(cs))
	for i, c := range cs {
		idx[c.ID] = i
	}
	depth := func(c Comment) int {
		d := 0
		for cur := c; !topLevel(cur.ParentID) && d < maxThreadDepth; d++ {
			i, ok := idx[cur.ParentID]
			if !ok {
				break
			}
			cur = cs[i]
		}
		return d
	}
	out := make([]Comment, len(cs))
	copy(out, cs)
	depths := make(map[string]int, len(cs))
	for _, c := range cs {
		depths[c.ID] = depth(c)
	}
	// stable insertion by depth keeps the source order inside one level
	sorted := make([]Comment, 0, len(out))
	for d := 0; len(sorted) < len(out) && d <= maxThreadDepth; d++ {
		for _, c := range out {
			if depths[c.ID] == d {
				sorted = append(sorted, c)
			}
		}
	}
	return sorted
}

// importComments writes the comments of every imported item. It never turns
// comments on. A post that cannot be written is reported and the step goes on;
// an unreadable id map stops the step, so nothing is imported twice.
func (r *Runner) importComments(ctx context.Context) error {
	total := 0
	for i := range r.dump.Items {
		total += len(r.dump.Items[i].Comments)
	}
	total += r.dump.CommentsDropped
	if total == 0 {
		return nil
	}
	if r.d.Comments == nil {
		r.res.Warnings = append(r.res.Warnings, "comments not imported: the server secret is not configured (set HATAMOTO_SECRET, or HATAMOTO_SECRETS_DIR to the directory holding secrets.env, and run the import again)")
		return nil
	}
	// One read of the id map. A failed read must stop the step: treating it as
	// empty would import every comment a second time.
	imported, err := r.st.All(ctx, KindComment)
	if err != nil {
		return fmt.Errorf("comments: read the id map: %w", err)
	}
	cr := &CommentsResult{SkippedNoPost: r.dump.CommentsDropped}
	r.res.Comments = cr

	done := 0
	for i := range r.dump.Items {
		it := &r.dump.Items[i]
		if len(it.Comments) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		done++
		r.say("comments", "%d/%d posts", done, len(r.dump.Items))
		r.importItemComments(ctx, it, cr, imported)
	}

	if set, err := comment.NewSettingsStore(r.d.DB.Pool()).Get(ctx); err == nil {
		cr.Enabled = set.Enabled
	} else {
		r.res.Warnings = append(r.res.Warnings, "comments: could not read the comment settings: "+err.Error())
	}
	return nil
}

func (r *Runner) importItemComments(ctx context.Context, it *Item, cr *CommentsResult, imported map[string]uuid.UUID) {
	info := r.infos[it.ID]
	var real []Comment
	for _, c := range it.Comments {
		switch {
		case isPlainComment(c.Type):
			real = append(real, c)
		case c.Type == "pingback" || c.Type == "trackback":
			cr.PingsTotal++
			if len(cr.Pings) < maxPingList {
				cr.Pings = append(cr.Pings, fmt.Sprintf("%s (#%s)", it.Title, c.ID))
			}
		default:
			cr.SkippedOtherType++
		}
	}
	if info == nil || info.ent == nil {
		cr.SkippedNoPost += len(real)
		return
	}

	var batch []comment.ImportedComment
	status := map[string]comment.Status{}
	for _, c := range orderThread(real) {
		if c.HadURL {
			cr.URLsDropped++
		}
		if !usableEmail(c.AuthorEmail) {
			cr.SkippedNoEmail++
			continue
		}
		var parentID *uuid.UUID
		if !topLevel(c.ParentID) {
			if pid, ok := imported[c.ParentID]; ok {
				parentID = &pid
			}
		}
		if _, ok := imported[c.ID]; ok {
			cr.AlreadyImported++
			continue
		}
		raw := commentHTMLToText(c.Content)
		res, err := commentfmt.Render(raw)
		if err != nil {
			if errors.Is(err, commentfmt.ErrEmpty) || errors.Is(err, commentfmt.ErrTooLong) {
				cr.SkippedBody++
				continue
			}
			r.commentsFailed(it, err)
			return
		}
		st, known := mapWPStatus(c.Approved)
		if !known {
			r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("comment #%s on %q has status %q: imported as pending", c.ID, it.Title, c.Approved))
		}
		parentKey := c.ParentID
		if topLevel(parentKey) {
			parentKey = ""
		}
		status[c.ID] = st
		batch = append(batch, comment.ImportedComment{
			Key: c.ID, ParentKey: parentKey, ParentID: parentID, EntityID: info.ent.ID,
			AuthorName: c.AuthorName, Email: c.AuthorEmail, IP: c.IP,
			BodyRaw: raw, BodyHTML: res.HTML, HasLinks: res.HasLinks, LinkDomains: res.LinkDomains, BodyHash: res.BodyHash,
			Status: st, CreatedAt: c.Date.UTC(),
		})
	}
	if len(batch) == 0 {
		return
	}
	stats, err := r.d.Comments.Import(ctx, batch)
	if err != nil {
		r.commentsFailed(it, err)
		return
	}
	for key, id := range stats.IDs {
		if err := r.st.Put(ctx, r.run.ID, KindComment, key, id, ""); err != nil {
			r.res.Warnings = append(r.res.Warnings, "id map: "+err.Error())
		}
		imported[key] = id
		cr.Imported++
		switch status[key] {
		case comment.StatusApproved:
			cr.Approved++
		case comment.StatusPending:
			cr.Pending++
		case comment.StatusSpam:
			cr.Spam++
		case comment.StatusTrash:
			cr.Trash++
		}
	}
	cr.CommentersCreated += stats.CommentersCreated
	cr.CommentersUpdated += stats.CommentersUpdated
	cr.Reparented += stats.Reparented
	cr.Clamped += stats.Clamped
}

// commentsFailed records a post whose comments could not be written. The error
// text comes from the importer and never carries comment content.
func (r *Runner) commentsFailed(it *Item, err error) {
	r.res.Errors = append(r.res.Errors, ItemError{Kind: "comments", ID: it.ID, Title: it.Title, Err: err.Error()})
}
