package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// builderSaveMaxBody bounds a save request: the largest doc plus its envelope.
const builderSaveMaxBody = 2 << 20

// Save operations of the builder.
const (
	opAutosave = "autosave"
	opSave     = "save"
	opPublish  = "publish"
)

type builderSaveRequest struct {
	Version int             `json:"version"`
	Doc     json.RawMessage `json:"doc"`
	Op      string          `json:"op"`
	Name    *string         `json:"name"`
}

type builderSaveReply struct {
	OK       bool            `json:"ok"`
	Version  int             `json:"version,omitempty"`
	SavedAt  string          `json:"savedAt,omitempty"`
	Status   string          `json:"status,omitempty"`
	Conflict bool            `json:"conflict,omitempty"`
	Code     string          `json:"code,omitempty"`
	Error    string          `json:"error,omitempty"`
	Note     string          `json:"note,omitempty"`
	Doc      json.RawMessage `json:"doc,omitempty"`
}

// saveProblem is a save the builder refuses, with the HTTP status it maps to.
type saveProblem struct {
	Status int
	Code   string
	Msg    string
	// Current is the stored version of a "conflict".
	Current int
}

// saveProblemOf phrases a refusal of the shared save as the builder shows it:
// the messages of the document rules are sentences.
func saveProblemOf(p *entity.DocProblem) *saveProblem {
	msg := p.Msg
	switch p.Code {
	case "invalid_page_doc", "invalid", "ref_cycle":
		msg = sentence(msg)
	}
	return &saveProblem{p.Status, p.Code, msg, 0}
}

func (p *saveProblem) Error() string { return p.Msg }

// savedPage is the outcome of a successful save.
type savedPage struct {
	// Entity is the page as stored after the save (and publish).
	Entity *entity.Entity
	// Note is something the person should know although the save worked: the
	// address was taken, or the page was saved but not published.
	Note string
}

// saveBuilderDoc stores doc as e's page_doc for op (autosave, save or publish)
// and, for publish, flips the page live. The caller has checked the version.
// It never writes page_sections: the entity service leaves the
// frozen mirror alone. A refusal is a *saveProblem.
func (a *Admin) saveBuilderDoc(ctx context.Context, actor *uuid.UUID, e *entity.Entity, doc json.RawMessage, op string, name *string) (*savedPage, error) {
	return a.saveBuilderDocAs(ctx, actor, e, doc, op, name, "")
}

// saveBuilderDocAs is saveBuilderDoc with the version's change summary named
// by the caller ("" keeps "Updated in the builder"; autosaves keep theirs).
func (a *Admin) saveBuilderDocAs(ctx context.Context, actor *uuid.UUID, e *entity.Entity, doc json.RawMessage, op string, name *string, changeSummary string) (*savedPage, error) {
	c := a.deps.Content
	if op != opAutosave && op != opSave && op != opPublish {
		return nil, &saveProblem{http.StatusBadRequest, "invalid_op", "Unknown save operation.", 0}
	}
	if op == opAutosave && e.Status != entity.StatusDraft {
		return nil, &saveProblem{http.StatusConflict, "not_draft", "Only drafts autosave. Use Update.", 0}
	}
	if op == opPublish && e.Status != entity.StatusDraft {
		return nil, &saveProblem{http.StatusConflict, "not_draft", "Only drafts can be published. Use Update.", 0}
	}
	title := e.Name
	if name != nil {
		if t := strings.TrimSpace(*name); t != "" {
			title = t
		}
	}
	// The rules and the write are the shared builder save; the admin adds its
	// own gates around them, in the order the builder has always reported them.
	res, prob, err := entity.SaveBuilderDoc(ctx, c.Entities, actor, e, doc, entity.SaveOpts{
		ChangeSummary:   changeSummary,
		Autosave:        op == opAutosave,
		Publish:         op == opPublish,
		ExpectedVersion: &e.Version,
		AfterParse: func(role string) *entity.DocProblem {
			if role != rolePage && op != opSave {
				return &entity.DocProblem{Status: http.StatusConflict, Code: "not_savable_op", Msg: "A " + role + " is saved explicitly with Save " + role + "; it is never published."}
			}
			return nil
		},
		BeforeWrite: func() *entity.DocProblem {
			if len([]rune(title)) > 255 {
				return &entity.DocProblem{Status: http.StatusUnprocessableEntity, Code: "invalid_name", Msg: "Use at most 255 characters for the title."}
			}
			if op == opPublish && untitledForPublish(title, e) {
				return &entity.DocProblem{Status: http.StatusUnprocessableEntity, Code: "untitled", Msg: "Give the page a title before publishing it."}
			}
			return nil
		},
		Tune: func(in *entity.UpdateEntityInput) { setPageName(in, e, title) },
		PublishGate: func(saved *entity.Entity) string {
			if generatedSlug(saved.Slug) {
				return "generated address"
			}
			return ""
		},
	})
	if prob != nil {
		return nil, saveProblemOf(prob)
	}
	if err != nil {
		var vc *entity.VersionConflictError
		switch {
		case errors.As(err, &vc):
			return nil, &saveProblem{http.StatusConflict, "conflict", staleMessage(e.Version, vc.Current), vc.Current}
		case errors.Is(err, entity.ErrEntityNotFound):
			return nil, &saveProblem{http.StatusNotFound, "not_found", "This page does not exist.", 0}
		}
		return nil, err
	}

	if op != opAutosave {
		// A saved page supersedes the actor's own recovery copy; other users' copies stay.
		a.dropRecovery(ctx, actor, e.ID)
	}

	out := &savedPage{Entity: res.Entity}
	parsed := res.Parsed
	if res.TakenSlug != "" {
		out.Note = "The address /" + res.TakenSlug + " is in use; the page keeps /" + res.Entity.Slug + ". Change it under Details."
	}
	switch {
	case res.PublishSkipped != "":
		out.Note = strings.TrimSpace("Saved, but not published. The page still has its generated address. Change it under Details to publish. " + out.Note)
	case res.PublishErr != nil:
		out.Note = "Saved, but the page could not be published: " + entityErrorText(res.PublishErr)
	}
	if op != opAutosave {
		if n := len(parsed.LightboxUnnamed()); n > 0 {
			out.Note = strings.TrimSpace(out.Note + " " + lightboxNote(n))
		}
	}
	// What the browser holds next is what is stored, not what the service returned.
	if fresh, err := c.Entities.GetEntity(ctx, e.ID); err == nil {
		out.Entity = fresh
	}
	return out, nil
}

// lightboxNote is the saved-page note for images that open in a lightbox and
// have no alt text, no caption and no accessible name.
func lightboxNote(n int) string {
	if n == 1 {
		return "Saved, but an image that opens in a lightbox has no alt text or caption, so screen readers cannot name its link. Add one."
	}
	return "Saved, but " + strconv.Itoa(n) + " images that open in a lightbox have no alt text or caption, so screen readers cannot name their links. Add one to each."
}

// storedDocDiffers reports whether the page_doc stored on e is not the posted
// doc, so the sanitiser (or the service) changed it on the way in.
func storedDocDiffers(e *entity.Entity, posted json.RawMessage) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(e.Attributes, &m) != nil {
		return true
	}
	var a, b any
	if json.Unmarshal(m["page_doc"], &a) != nil || json.Unmarshal(posted, &b) != nil {
		return true
	}
	return !reflect.DeepEqual(a, b)
}

// handleBuilderSave is the builder's save endpoint: autosave for drafts, Update
// for published pages, Publish for drafts. The version the browser holds must be
// the stored one; nothing is written otherwise.
func (a *Admin) handleBuilderSave(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	reply := func(status int, v builderSaveReply) { a.writeJSON(w, status, v) }
	r.Body = http.MaxBytesReader(w, r.Body, builderSaveMaxBody)
	var req builderSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reply(http.StatusRequestEntityTooLarge, builderSaveReply{Code: "too_large", Error: "This page is too large to save."})
			return
		}
		reply(http.StatusBadRequest, builderSaveReply{Code: "bad_request", Error: "Invalid request body."})
		return
	}
	// A missing version is stale: only the version the page was opened at
	// proves nobody saved in between.
	if req.Version != e.Version {
		reply(http.StatusConflict, builderSaveReply{Conflict: true, Version: e.Version, Error: staleMessage(req.Version, e.Version)})
		return
	}
	saved, err := a.saveBuilderDoc(r.Context(), actorID(r), e, req.Doc, req.Op, req.Name)
	if err != nil {
		var p *saveProblem
		if errors.As(err, &p) {
			if p.Code == "conflict" { // another save landed between the version check and the write
				reply(http.StatusConflict, builderSaveReply{Conflict: true, Version: p.Current, Error: p.Msg})
				return
			}
			rep := builderSaveReply{Code: p.Code, Error: p.Msg}
			if p.Status == http.StatusConflict {
				rep.Version = e.Version
			}
			reply(p.Status, rep)
			return
		}
		a.logger().Error("admin: builder save failed", "err", err)
		reply(http.StatusInternalServerError, builderSaveReply{Code: "server_error", Error: "The page could not be saved."})
		return
	}
	rep := builderSaveReply{
		OK:      true,
		Version: saved.Entity.Version,
		SavedAt: saved.Entity.UpdatedAt.UTC().Format(time.RFC3339),
		Status:  string(saved.Entity.Status),
		Note:    saved.Note,
	}
	if storedDocDiffers(saved.Entity, req.Doc) {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(saved.Entity.Attributes, &m)
		rep.Doc = m["page_doc"]
	}
	reply(http.StatusOK, rep)
}
