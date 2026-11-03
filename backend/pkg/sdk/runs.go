package sdk

import (
	"encoding/json"
	"fmt"
	"slices"
)

// A run is a plugin-driven job the owner starts: a fetch phase that collects
// data into a buffer, then a transform phase that stages content into a
// changeset. The host calls the plugin's on_run_step export until the plugin
// calls run.finish in both phases.
const (
	RunPhaseFetch     = "fetch"
	RunPhaseTransform = "transform"

	// RunStepExport is the export the host calls for every step of a run.
	RunStepExport = "on_run_step"

	MaxRuns      = 8
	MaxRunInputs = 16
)

// Proposal kinds a run may declare in Run.Proposes.
const (
	ProposeSiteSettings = "site_settings"
	ProposeURLPatterns  = "url_patterns"
	ProposeHomePage     = "home_page"
	ProposeMenuLocation = "menu_location"
)

// proposalKinds lists every valid Run.Proposes entry.
var proposalKinds = []string{ProposeSiteSettings, ProposeURLPatterns, ProposeHomePage, ProposeMenuLocation}

// Run is one run a plugin declares in the "runs" block of plugin.json.
type Run struct {
	// Name is the run's identifier, unique within the plugin.
	Name string `json:"name"`
	// Title is shown to the owner when starting the run.
	Title string `json:"title"`
	// Inputs are the values the owner supplies at start.
	Inputs []RunInput `json:"inputs,omitempty"`
	// Stages must be ["fetch", "transform"].
	Stages []string `json:"stages"`
	// Proposes lists the proposal kinds the run may submit with
	// changeset.propose.
	Proposes []string `json:"proposes,omitempty"`
}

// RunInput is one value the owner supplies when starting a run.
type RunInput struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type,omitempty"` // text (default), url, number, bool or select
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"` // the choices of a select
}

func (m Manifest) validateRuns() error {
	if len(m.Runs) == 0 {
		return nil
	}
	if len(m.Runs) > MaxRuns {
		return fmt.Errorf("sdk: plugin %q: more than %d runs", m.Name, MaxRuns)
	}
	seen := map[string]bool{}
	for _, r := range m.Runs {
		switch {
		case !identPattern.MatchString(r.Name):
			return fmt.Errorf("sdk: plugin %q: run name %q (want %s)", m.Name, r.Name, identPattern)
		case seen[r.Name]:
			return fmt.Errorf("sdk: plugin %q: duplicate run %q", m.Name, r.Name)
		case r.Title == "":
			return fmt.Errorf("sdk: plugin %q: run %q: title is required", m.Name, r.Name)
		case !slices.Equal(r.Stages, []string{RunPhaseFetch, RunPhaseTransform}):
			return fmt.Errorf("sdk: plugin %q: run %q: stages must be [%q, %q]", m.Name, r.Name, RunPhaseFetch, RunPhaseTransform)
		case len(r.Inputs) > MaxRunInputs:
			return fmt.Errorf("sdk: plugin %q: run %q: more than %d inputs", m.Name, r.Name, MaxRunInputs)
		}
		seen[r.Name] = true
		for _, k := range r.Proposes {
			if !slices.Contains(proposalKinds, k) {
				return fmt.Errorf("sdk: plugin %q: run %q: unknown proposal kind %q", m.Name, r.Name, k)
			}
		}
		inputs := map[string]bool{}
		for _, in := range r.Inputs {
			if !identPattern.MatchString(in.Name) || inputs[in.Name] {
				return fmt.Errorf("sdk: plugin %q: run %q: bad or duplicate input %q (want %s)", m.Name, r.Name, in.Name, identPattern)
			}
			inputs[in.Name] = true
			if in.Label == "" {
				return fmt.Errorf("sdk: plugin %q: run %q: input %q needs a label", m.Name, r.Name, in.Name)
			}
		}
	}
	return nil
}

// RunStep is the input of an on_run_step call.
type RunStep struct {
	RunID  string `json:"run_id"`
	Phase  string `json:"phase"`  // RunPhaseFetch or RunPhaseTransform
	Cursor string `json:"cursor"` // the last run.checkpoint of this phase, "" at the start
}

// RunStepResult is the output of on_run_step. Progress and completion are
// reported through run.* host calls, so the result carries nothing today.
type RunStepResult struct{}

// RunInfo is the result of run.info.
type RunInfo struct {
	RunID  string          `json:"run_id"`
	Name   string          `json:"name"`
	Phase  string          `json:"phase"`
	Cursor string          `json:"cursor"`
	Params json.RawMessage `json:"params,omitempty"` // the owner's inputs, keyed by input name
	Hosts  []string        `json:"hosts,omitempty"`  // the hosts the owner confirmed for http.fetch and media.import_url
	// UploadID is the owner's uploaded file for upload.read, "" when none.
	UploadID string `json:"upload_id,omitempty"`
}

// RunCheckpointArgs are the arguments of run.checkpoint (at most 4 KiB).
type RunCheckpointArgs struct {
	Cursor string `json:"cursor"`
}

// RunProgressArgs are the arguments of run.progress.
type RunProgressArgs struct {
	Done    int    `json:"done"`
	Total   int    `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}

// RunFinishArgs are the arguments of run.finish. Summary is shown to the
// owner with the changeset.
type RunFinishArgs struct {
	Summary json.RawMessage `json:"summary,omitempty"`
}

// RunFailArgs are the arguments of run.fail.
type RunFailArgs struct {
	Reason string `json:"reason"`
}

// UploadReadArgs are the arguments of upload.read; Len is at most 256 KiB.
type UploadReadArgs struct {
	UploadID string `json:"upload_id"`
	Offset   int64  `json:"offset"`
	Len      int64  `json:"len"`
}

// UploadChunk is the result of upload.read.
type UploadChunk struct {
	DataB64 string `json:"data_b64"`
	Size    int64  `json:"size"` // the whole upload
	EOF     bool   `json:"eof"`
}

// BufferPutArgs are the arguments of buffer.put.
type BufferPutArgs struct {
	Key      string `json:"key"`
	ValueB64 string `json:"value_b64"`
}

// BufferGetArgs are the arguments of buffer.get.
type BufferGetArgs struct {
	Key string `json:"key"`
}

// BufferValue is the result of buffer.get.
type BufferValue struct {
	Found    bool   `json:"found"`
	ValueB64 string `json:"value_b64,omitempty"`
}

// BufferListArgs are the arguments of buffer.list.
type BufferListArgs struct {
	Prefix string `json:"prefix,omitempty"`
	After  string `json:"after,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// BufferKeys is the result of buffer.list; Next is the After value of the
// next page, "" at the end.
type BufferKeys struct {
	Keys []string `json:"keys"`
	Next string   `json:"next,omitempty"`
}

// MediaImportURLArgs are the arguments of media.import_url.
type MediaImportURLArgs struct {
	URL      string `json:"url"`
	Filename string `json:"filename,omitempty"`
}

// MediaHandle is the result of media.import_url: a staged file to pass to
// changeset.media.attach.
type MediaHandle struct {
	Handle string `json:"handle"`
}

// SiteInfo is the result of site.info.
type SiteInfo struct {
	Title       string   `json:"title"`
	Tagline     string   `json:"tagline,omitempty"`
	TimeZone    string   `json:"time_zone"`
	Language    string   `json:"language"`
	Locales     []string `json:"locales,omitempty"`
	URLPatterns []string `json:"url_patterns,omitempty"`
}

// SlugTakenArgs are the arguments of entities.slug_taken.
type SlugTakenArgs struct {
	Schema string `json:"schema"`
	Slug   string `json:"slug"`
	Locale string `json:"locale,omitempty"`
}

// SlugTaken is the result of entities.slug_taken.
type SlugTaken struct {
	Taken bool `json:"taken"`
}

// SEO is the search-engine metadata of an entity upsert.
type SEO struct {
	MetaTitle       string `json:"meta_title,omitempty"`
	MetaDescription string `json:"meta_description,omitempty"`
	Canonical       string `json:"canonical,omitempty"`
	Robots          string `json:"robots,omitempty"`
}

// EntityUpsert stages the creation or update of an entity. SourceKey is the
// plugin's own stable key for the object: the same key on a later run updates
// the entity the first run created. ParentKey names an earlier staged or
// mapped entity by its source key.
type EntityUpsert struct {
	SourceKey  string          `json:"source_key"`
	Schema     string          `json:"schema"`
	Slug       string          `json:"slug,omitempty"`
	ParentKey  string          `json:"parent_key,omitempty"`
	Locale     string          `json:"locale,omitempty"`
	Name       string          `json:"name"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
	SEO        *SEO            `json:"seo,omitempty"`
}

// TermUpsert stages a category, tag or other term.
type TermUpsert struct {
	SourceKey   string `json:"source_key"`
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PersonUpsert stages a byline (an author shown on content, not a user account).
type PersonUpsert struct {
	SourceKey string `json:"source_key"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Bio       string `json:"bio,omitempty"`
}

// MediaAttach stages a media item from a staged handle (media.import_url).
type MediaAttach struct {
	SourceKey string `json:"source_key"`
	Handle    string `json:"handle"`
	Alt       string `json:"alt,omitempty"`
}

// MenuUpsert stages a menu and its items.
type MenuUpsert struct {
	SourceKey string     `json:"source_key"`
	Slug      string     `json:"slug"`
	Name      string     `json:"name"`
	Items     []MenuItem `json:"items,omitempty"`
}

// MenuItem is one item of a MenuUpsert. EntityKey names a staged or mapped
// entity by its source key; URL is for an external or fixed link.
type MenuItem struct {
	Label     string     `json:"label"`
	URL       string     `json:"url,omitempty"`
	EntityKey string     `json:"entity_key,omitempty"`
	Children  []MenuItem `json:"children,omitempty"`
}

// RedirectUpsert stages a redirect. Source may not be a live path.
type RedirectUpsert struct {
	SourceKey  string `json:"source_key"`
	From       string `json:"from"`
	To         string `json:"to"`
	StatusCode int    `json:"status_code,omitempty"`
}

// ChangesetDeleteArgs are the arguments of changeset.delete.
type ChangesetDeleteArgs struct {
	Kind      string `json:"kind"` // entity, term, menu, media or redirect
	SourceKey string `json:"source_key"`
}

// UpsertResult is the result of every changeset.* call that stages an item.
type UpsertResult struct {
	SourceKey string `json:"source_key"`
	// Created is true when no earlier run mapped this source key.
	Created bool `json:"created"`
}

// Proposal is the argument of changeset.propose: a change to a site setting
// the owner signs off separately. Kind must be listed in the run's proposes.
type Proposal struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}
