package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// firstReleaseSummary is the placeholder summary of a release with no previous
// release to diff against; the owner writes it up in the CMS.
const firstReleaseSummary = "First Hatamoto 1.x release. Notes to be written."

// pendingPublish marks a draft the generator created and has not published yet.
// It is not a note field, so it is not in the notes hash.
const pendingPublish = "pending_publish"

// Opts says what to sync. SummaryOnly writes just the placeholder summary and
// no PR list. Channel and Yanked are tri-state: empty / nil keeps the stored
// value of an existing entry. A new entry needs a Channel and starts not yanked.
type Opts struct {
	Version, Channel string
	Yanked           *bool
	SummaryOnly      bool
	DryRun           bool
}

// Result is what Sync did (or, with DryRun, would do).
type Result struct {
	Action string
	Note   string // set when something was deliberately left alone
}

// Sync makes the entry for o.Version match the repo: it creates a missing
// entry, refreshes the notes of an unedited one, and only ever touches the
// metadata (channel, yanked, date) of one whose notes were edited by hand.
// Channel and yanked are only written when o says so.
func Sync(c *Client, g gitRunner, o Opts) (Result, error) {
	if o.Channel != "" && rankOf(o.Channel) < 0 {
		return Result{}, fmt.Errorf("channel %q must be canary, edge or stable", o.Channel)
	}
	sortKey, err := VersionSort(o.Version)
	if err != nil {
		return Result{}, err
	}
	date, err := g("log", "-1", "--format=%cI", o.Version)
	if err != nil {
		return Result{}, err
	}
	notes, err := generate(g, o)
	if err != nil {
		return Result{}, err
	}
	meta := map[string]any{
		"version": o.Version, "version_sort": sortKey, "released_at": releasedAt(date),
	}
	if o.Channel != "" {
		meta["channel"] = o.Channel
	}
	if o.Yanked != nil {
		meta["yanked"] = *o.Yanked
	}

	ent, err := c.Find(o.Version)
	if err != nil {
		return Result{}, err
	}
	if ent == nil {
		if o.Channel == "" {
			return Result{}, fmt.Errorf("%s has no entry yet; a channel is required to create it", o.Version)
		}
		if o.Yanked == nil {
			meta["yanked"] = false
		}
		if !o.DryRun {
			if _, err := c.Create(o.Version, withNotes(meta, notes)); err != nil {
				return Result{}, err
			}
		}
		return Result{Action: "create"}, nil
	}

	// Only a draft this tool created and never published (pending_publish) is
	// published here. Any other non-published status was an owner's choice and
	// is left alone; the run's metadata and notes updates still apply.
	cur := ent.Attributes
	pending, _ := cur[pendingPublish].(bool)
	var res Result
	switch {
	case ent.Status == "draft" && pending:
		if !o.DryRun {
			if err := c.Publish(ent.ID); err != nil {
				return Result{}, err
			}
		}
		res.Action = "publish"
		meta[pendingPublish] = false
	case ent.Status != "published":
		res.Note = fmt.Sprintf("entry is %s; status left alone", ent.Status)
	case pending: // published, but the create never got to clear the flag
		meta[pendingPublish] = false
	}

	// The date is set once; a hand correction in the admin stays.
	if d, _ := cur["released_at"].(string); d != "" {
		delete(meta, "released_at")
	}
	edited := notesFromAttrs(cur).Hash() != cur["generated_hash"]
	// Clearing the flag is bookkeeping; it is written along with any other
	// update but alone it is not reported.
	clearFlag := meta[pendingPublish] == false && pending
	delete(meta, pendingPublish)
	metaChanged := !attrsEqual(cur, meta)
	if clearFlag {
		meta[pendingPublish] = false
	}
	var next Result
	switch {
	case !edited && notes.Hash() != cur["generated_hash"]:
		next, err = update(c, ent.ID, withNotes(meta, notes), "update-notes", o.DryRun)
	case metaChanged:
		next, err = update(c, ent.ID, meta, "update-meta", o.DryRun)
	case clearFlag:
		_, err = update(c, ent.ID, meta, "", o.DryRun)
		next = Result{Action: "noop"}
	case edited:
		next = Result{Action: "skip-edited"}
	default:
		next = Result{Action: "noop"}
	}
	if err != nil {
		return Result{}, err
	}
	switch {
	case res.Action == "":
		res.Action = next.Action
	case next.Action != "noop" && next.Action != "skip-edited":
		res.Action += "+" + next.Action
	}
	return res, nil
}

// releasedAt is the calendar day of a %cI timestamp in its own offset, as
// YYYY-MM-DD (the form the admin field documents).
func releasedAt(cI string) string {
	cI = strings.TrimSpace(cI)
	if len(cI) >= 10 {
		return cI[:10]
	}
	return cI
}

func generate(g gitRunner, o Opts) (Notes, error) {
	if o.SummaryOnly {
		return Notes{Summary: firstReleaseSummary}, nil
	}
	prev, ok := PrevTag(g, o.Version)
	if !ok {
		return Notes{}, fmt.Errorf("%s has no previous vX.Y.Z tag; use --summary-only", o.Version)
	}
	prs, err := ListPRs(g, prev, o.Version)
	if err != nil {
		return Notes{}, err
	}
	return Group(prs), nil
}

func update(c *Client, id string, attrs map[string]any, action string, dry bool) (Result, error) {
	if !dry {
		if err := c.UpdateAttrs(id, attrs); err != nil {
			return Result{}, err
		}
	}
	return Result{Action: action}, nil
}

// withNotes returns a copy of meta carrying the notes and their hash.
func withNotes(meta map[string]any, n Notes) map[string]any {
	out := map[string]any{}
	for k, v := range meta {
		out[k] = v
	}
	out["summary"] = n.Summary
	out["features"], out["fixes"], out["docs"], out["chores"] = nonNil(n.Features), nonNil(n.Fixes), nonNil(n.Docs), nonNil(n.Chores)
	out["generated_hash"] = n.Hash()
	return out
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// notesFromAttrs reads the five note fields back from stored attributes.
func notesFromAttrs(a map[string]any) Notes {
	n := Notes{}
	n.Summary, _ = a["summary"].(string)
	for key, dst := range map[string]*[]string{"features": &n.Features, "fixes": &n.Fixes, "docs": &n.Docs, "chores": &n.Chores} {
		if l, ok := a[key].([]any); ok {
			for _, v := range l {
				if s, ok := v.(string); ok {
					*dst = append(*dst, s)
				}
			}
		}
	}
	return n
}

// attrsEqual reports whether cur holds every key of want with an equal value,
// compared through JSON so decoded numbers and freshly built values compare alike.
func attrsEqual(cur, want map[string]any) bool {
	for k := range want {
		var a, b any
		ca, _ := json.Marshal(cur[k])
		cb, _ := json.Marshal(want[k])
		json.Unmarshal(ca, &a)
		json.Unmarshal(cb, &b)
		if _, ok := cur[k]; !ok || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
