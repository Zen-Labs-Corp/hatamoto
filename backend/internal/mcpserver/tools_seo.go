package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/seopanel"
	"hatamoto.dev/core/internal/seosvc"
)

const (
	seoRead  = hmauth.ScopeReadSEO
	seoWrite = hmauth.ScopeWriteSEO
)

// seoError makes a refused patch and a missing entry readable by the agent.
func seoError(err error) error {
	var ve *seosvc.ValidationError
	switch {
	case errors.As(err, &ve):
		return userErr("invalid: %s", ve.Error()[len("validation failed: "):])
	case errors.Is(err, seosvc.ErrNotFound):
		return userErr("entry not found or not published")
	}
	return err
}

func seoChecks(cs []seopanel.Check) []object {
	out := make([]object, 0, len(cs))
	for _, c := range cs {
		out = append(out, object{"id": c.ID, "name": c.Name, "status": c.Status, "message": c.Message})
	}
	return out
}

// serpChecks are the two search-result length checks. They count in the score
// like the search checks, so they are listed with them.
func serpChecks(p seosvc.Panel) []object {
	return seoChecks([]seopanel.Check{
		{ID: "title_length", Name: "Title length", Status: p.SERP.TitleStatus, Message: p.SERP.TitleMsg},
		{ID: "description_length", Name: "Description length", Status: p.SERP.DescStatus, Message: p.SERP.DescMsg},
	})
}

func (s *Server) registerSEO() {
	svc := func() (seosvc.Service, error) {
		if s.d.SEO == nil {
			return nil, userErr("seo tools are not available")
		}
		return s.d.SEO, nil
	}

	type none struct{}
	register(s, toolSpec{name: "seo_get", description: "The site's SEO settings: every section (separator, home, types, taxonomy, archives, variables, social, index, robots, verification, sitemap, breadcrumbs, indexnow, monitor404), type_settings per content type and ai_crawlers. Values are stored values (monitor404.disabled, not enabled). The IndexNow key is never shown.", scope: seoRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, _ none) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			return sv.Get(ctx)
		})

	type updateIn struct {
		Patch object `json:"patch" jsonschema:"sections to change, keyed like seo_get's result (a section name, type_settings or ai_crawlers)"`
	}
	register(s, toolSpec{name: "seo_update", description: "Change SEO settings with a partial patch: only the keys you send change, and each section named is merged into the stored one (send a field to set it, null on a map entry deletes it, ai_crawlers.denied_purposes replaces the list). Send the keys as seo_get returns them. A refused patch (field errors) changes nothing. Returns the whole document.", scope: seoWrite, write: true, idempotent: true},
		func(ctx context.Context, key *hmauth.APIKey, in updateIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			patch := make(map[string]json.RawMessage, len(in.Patch))
			for k, v := range in.Patch {
				raw, err := json.Marshal(v)
				if err != nil {
					return nil, userErr("patch.%s is not valid JSON", k)
				}
				patch[k] = raw
			}
			doc, err := sv.Update(ctx, seosvc.Actor{Via: "mcp", KeyName: key.Name, KeyID: key.ID.String()}, patch)
			if err != nil {
				return nil, seoError(err)
			}
			return doc, nil
		})

	type entityIn struct {
		EntityID string `json:"entity_id" jsonschema:"id of the entity"`
	}
	register(s, toolSpec{name: "seo_preview", description: "The SEO tags a published entry's public page carries: title, description, canonical, robots, Open Graph and Twitter tags, as a visitor's page emits them. Drafts are not found.", scope: seoRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in entityIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			id, err := parseID("entity_id", in.EntityID)
			if err != nil {
				return nil, err
			}
			p, err := sv.Preview(ctx, id)
			if err != nil {
				return nil, seoError(err)
			}
			return p, nil
		})

	register(s, toolSpec{name: "seo_analyze", description: "The SEO checks of an entry as stored (search and answer checks, without keyword or llms.txt checks): each check's status and message, the 0-100 score, its tone (pass, warn, fail) and how many checks need fixing.", scope: seoRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in entityIn) (any, error) {
			sv, err := svc()
			if err != nil {
				return nil, err
			}
			id, err := parseID("entity_id", in.EntityID)
			if err != nil {
				return nil, err
			}
			p, err := sv.Analyze(ctx, id)
			if err != nil {
				return nil, seoError(err)
			}
			return object{
				"seo_checks": append(serpChecks(p), seoChecks(p.SEO)...), "answer_checks": seoChecks(p.Answer),
				"score": p.Score(), "tone": p.ScoreTone(), "to_fix": p.ToFix(),
				"pass": p.Pass, "warn": p.Warn, "fail": p.Fail, "total": p.Total,
				"words": p.Words, "reading_minutes": p.ReadingMinutes, "faq": p.FAQ,
			}, nil
		})

	type notFoundIn struct {
		Search  string `json:"search,omitempty" jsonschema:"part of the address"`
		Visitor string `json:"visitor,omitempty" jsonschema:"people or bots (default: all)"`
		Page    int    `json:"page,omitempty" jsonschema:"page number from 1"`
		PerPage int    `json:"per_page,omitempty" jsonschema:"rows per page (default 20, max 100)"`
	}
	register(s, toolSpec{name: "seo_404_list", description: "The 404 log: addresses visitors asked for that did not exist, with hits, last seen, referrer and whether a redirect covers them.", scope: seoRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in notFoundIn) (any, error) {
			if s.d.NotFound == nil {
				return nil, userErr("seo tools are not available")
			}
			if in.Visitor != "" && in.Visitor != redirect.VisitorPeople && in.Visitor != redirect.VisitorBots {
				return nil, userErr("visitor must be %q, %q or empty", redirect.VisitorPeople, redirect.VisitorBots)
			}
			page := in.Page
			if page < 1 {
				page = 1
			}
			items, total, err := s.d.NotFound.List404Entries(ctx, redirect.NotFoundListParams{
				Search: in.Search, Visitor: in.Visitor, Page: page, PerPage: clampLimit(in.PerPage, 20)})
			if err != nil {
				return nil, fmt.Errorf("list 404: %w", err)
			}
			if items == nil {
				items = []*redirect.NotFoundEntry{}
			}
			return object{"items": items, "total": total}, nil
		})
}
