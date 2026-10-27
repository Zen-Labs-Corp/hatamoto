package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Findings from the server handlers (backend/internal/server/entity_handlers.go),
// confirmed 2026-10-19 and the reason the lookup below is "list everything":
//
//   - GET /api/v1/entities filters only by schema_id, status and search
//     (a name/slug ILIKE). There is no slug, parent or under filter.
//     Offset paging (page=N&limit<=100) answers {items,total,page,limit,total_pages};
//     each item carries id, schema_id, name, slug, status, attributes, parent_id.
//   - POST /api/v1/entities takes CreateEntityInput (schema_id, name, slug,
//     attributes, parent_id) and answers 201 with the entity.
//   - PATCH /api/v1/entities/{id} takes UpdateEntityInput (name, attributes
//     replace the stored values) and answers 200 with the entity.
//   - PATCH /api/v1/entities/{id}/status takes {"status":"published"}.
//   - Slug clash answers 409 {"error","code":"slug_exists"}. With a {path} URL
//     pattern a slug only has to be unique among siblings.
//   - An API key goes in the X-API-Key header on all of these (keyRoutes in
//     backend/internal/middleware/apikey_route_auth.go); schemas and menus are
//     not reachable with a key, menus go through MCP.

// Client is a small REST client for the entity API. The key is held in an
// unexported field and only ever written to the X-API-Key header.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Delay is the pause after every write, to stay under the key rate limit.
	Delay time.Duration
	// Sleep replaces time.Sleep in tests.
	Sleep func(time.Duration)

	key string
}

// NewClient builds a client for cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		BaseURL: cfg.URL,
		HTTP:    newHTTPClient(),
		Delay:   150 * time.Millisecond,
		key:     cfg.APIKey,
	}
}

// newHTTPClient returns a client that never follows a redirect: the key is a
// custom header that Go would copy to any redirect target, including another
// host or plain http, and a redirected POST would silently turn into a GET.
// It has its own transport: http.DefaultTransport is shared by the whole process,
// and anything that closes its idle connections (httptest.Server.Close does)
// would break a request in flight.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return fmt.Errorf("the site redirected to %s; set HATAMOTO_URL to the final address (the API key is never sent to a redirect)", req.URL.Redacted())
		},
	}
}

// APIError is a non-2xx answer from the site.
type APIError struct {
	Status int
	Code   string
	Msg    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("site answered %d %s: %s", e.Status, e.Code, e.Msg)
	}
	return fmt.Sprintf("site answered %d: %s", e.Status, e.Msg)
}

// Entity is the part of the entity JSON this tool reads.
type Entity struct {
	ID         string          `json:"id"`
	SchemaID   string          `json:"schema_id"`
	Name       string          `json:"name"`
	Slug       string          `json:"slug"`
	Status     string          `json:"status"`
	ParentID   string          `json:"parent_id,omitempty"`
	Attributes json.RawMessage `json:"attributes"`
}

// SourceHash reads attributes.source_hash.
func (e Entity) SourceHash() string {
	var a struct {
		SourceHash string `json:"source_hash"`
	}
	_ = json.Unmarshal(e.Attributes, &a)
	return a.SourceHash
}

// DocAttributes are the attributes written for a doc entity.
type DocAttributes struct {
	Content     string `json:"content"`
	Description string `json:"description"`
	NavTitle    string `json:"nav_title"`
	SourceHash  string `json:"source_hash"`
}

type createReq struct {
	SchemaID   string        `json:"schema_id"`
	Name       string        `json:"name"`
	Slug       string        `json:"slug"`
	Attributes DocAttributes `json:"attributes"`
	ParentID   string        `json:"parent_id,omitempty"`
}

type updateReq struct {
	Name       string        `json:"name"`
	Attributes DocAttributes `json:"attributes"`
}

type pageResp struct {
	Items      []Entity `json:"items"`
	TotalPages int      `json:"total_pages"`
}

// ListEntities returns every entity of a schema (all statuses), paging by
// offset. With an empty schemaID the search term narrows the list instead.
func (c *Client) ListEntities(ctx context.Context, schemaID, search string) ([]Entity, error) {
	var all []Entity
	for page := 1; ; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {"100"}}
		if schemaID != "" {
			q.Set("schema_id", schemaID)
		}
		if search != "" {
			q.Set("search", search)
		}
		var r pageResp
		if err := c.do(ctx, http.MethodGet, "/api/v1/entities?"+q.Encode(), nil, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Items...)
		if page >= r.TotalPages || len(r.Items) == 0 {
			return all, nil
		}
	}
}

// CreateEntity creates a draft doc entity.
func (c *Client) CreateEntity(ctx context.Context, schemaID, parentID, slug, name string, a DocAttributes) (Entity, error) {
	var e Entity
	err := c.do(ctx, http.MethodPost, "/api/v1/entities", createReq{SchemaID: schemaID, Name: name, Slug: slug, Attributes: a, ParentID: parentID}, &e)
	return e, err
}

// UpdateEntity replaces the name and the whole attributes object.
func (c *Client) UpdateEntity(ctx context.Context, id, name string, a DocAttributes) (Entity, error) {
	var e Entity
	err := c.do(ctx, http.MethodPatch, "/api/v1/entities/"+id, updateReq{Name: name, Attributes: a}, &e)
	return e, err
}

// SetStatus changes the status (published, draft, ...).
func (c *Client) SetStatus(ctx context.Context, id, status string) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/entities/"+id+"/status", map[string]string{"status": status}, nil)
}

const maxRetries = 5

// do sends one request, retrying 429 answers after Retry-After. Writes are
// followed by Delay.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("X-API-Key", c.key)
		req.Header.Set("Accept", "application/json")
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return redactURL(err)
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if rerr != nil {
			return rerr
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
				wait = time.Duration(s) * time.Second
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return apiError(resp.StatusCode, body)
		}
		if method != http.MethodGet && c.Delay > 0 {
			if err := c.sleep(ctx, c.Delay); err != nil {
				return err
			}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("%s %s: unreadable answer: %w", method, path, err)
		}
		return nil
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		c.Sleep(d)
		return ctx.Err()
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func apiError(status int, body []byte) error {
	var e struct{ Error, Code string }
	if json.Unmarshal(body, &e) != nil || e.Error == "" {
		e.Error = string(bytes.TrimSpace(body))
		if len(e.Error) > 200 {
			e.Error = e.Error[:200]
		}
	}
	return &APIError{Status: status, Code: e.Code, Msg: e.Error}
}

// redactURL drops the request URL from transport errors (it holds nothing
// secret, but the message stays short and uniform).
func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}
