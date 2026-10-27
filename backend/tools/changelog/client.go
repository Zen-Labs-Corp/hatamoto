package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to the entities REST API with an API key. The key is only ever
// sent in the X-API-Key header and is scrubbed from error text.
type Client struct {
	Base, Key string
	HTTP      *http.Client
	SchemaID  string
}

// Entity is the part of an entity the generator reads.
type Entity struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Slug       string         `json:"slug"`
	Status     string         `json:"status"`
	Attributes map[string]any `json:"attributes"`
}

// do sends one request and decodes a JSON response into out (if non-nil).
func (c *Client) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return c.scrub(err)
	}
	req.Header.Set("X-API-Key", c.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Never follow a redirect: it would turn a PATCH/POST into a GET that
	// "succeeds" and forward the key to wherever Location points.
	hc := &http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		cp := *c.HTTP
		hc = &cp
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return c.scrub(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return c.scrub(err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		return c.scrub(fmt.Errorf("%s %s: HTTP %d redirect to %q; set HATAMOTO_URL to the final https URL",
			method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, resp.Header.Get("Location")))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return c.scrub(fmt.Errorf("%s %s: HTTP %d: %s", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, msg))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", method, strings.SplitN(path, "?", 2)[0], err)
		}
	}
	return nil
}

// scrub removes the key from an error, whatever produced it.
func (c *Client) scrub(err error) error {
	if c.Key == "" || !strings.Contains(err.Error(), c.Key) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), c.Key, "***"))
}

// Find returns the entry whose attributes.version is version, or nil. It lists
// the whole schema (one entry per release, a few pages) and compares the
// version exactly: a server search would miss an entry renamed in the admin,
// and hide a duplicate of it. Two exact matches are an error: updating either
// one would be a guess.
func (c *Client) Find(version string) (*Entity, error) {
	items, err := c.list()
	if err != nil {
		return nil, err
	}
	var found []*Entity
	for i := range items {
		if v, _ := items[i].Attributes["version"].(string); v == version {
			found = append(found, &items[i])
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%d entries have version %s (ids %s, %s, ...); delete the duplicates in the admin",
		len(found), version, found[0].ID, found[1].ID)
}

// list returns every entry of the schema, reading pages until the server says
// there are no more. A full page with no page count is treated as possibly
// incomplete; maxPages stops a server that ignores page.
func (c *Client) list() ([]Entity, error) {
	const limit, maxPages = 100, 100
	var all []Entity
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("listing entities: more than %d pages; the server may be ignoring page", maxPages)
		}
		q := url.Values{"schema_id": {c.SchemaID}, "page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(limit)}}
		var res struct {
			Items      []Entity `json:"items"`
			TotalPages int      `json:"total_pages"`
		}
		if err := c.do(http.MethodGet, "/api/v1/entities?"+q.Encode(), nil, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		more := page < res.TotalPages || (res.TotalPages == 0 && len(res.Items) == limit)
		if !more || len(res.Items) == 0 {
			return all, nil
		}
	}
}

// Create makes the entry and publishes it. Create always stores a draft, so
// publishing is a second call. The draft carries pending_publish until it is
// published, so a later sync can tell a draft stranded here from one an owner
// unpublished. The slug is left to the server, which turns the
// dots of a version into dashes.
func (c *Client) Create(name string, attrs map[string]any) (*Entity, error) {
	var e Entity
	pending := map[string]any{pendingPublish: true}
	for k, v := range attrs {
		pending[k] = v
	}
	err := c.do(http.MethodPost, "/api/v1/entities", map[string]any{
		"schema_id": c.SchemaID, "name": name, "attributes": pending,
	}, &e)
	if err != nil {
		return nil, err
	}
	if e.ID == "" {
		return nil, fmt.Errorf("create %s: the response carried no entity id", name)
	}
	if err := c.Publish(e.ID); err != nil {
		return nil, err
	}
	e.Status = "published"
	if err := c.UpdateAttrs(e.ID, map[string]any{pendingPublish: false}); err != nil {
		return nil, fmt.Errorf("published, but clearing %s failed (the next sync retries): %w", pendingPublish, err)
	}
	return &e, nil
}

// Publish sets an entry's status to published.
func (c *Client) Publish(id string) error {
	return c.do(http.MethodPatch, "/api/v1/entities/"+url.PathEscape(id)+"/status",
		map[string]string{"status": "published"}, nil)
}

// UpdateAttrs sets the given attributes on an entry and keeps the rest. A PATCH
// replaces the whole attributes object, so the current ones are fetched, merged
// and sent in full: a partial object would wipe hand edits.
func (c *Client) UpdateAttrs(id string, attrs map[string]any) error {
	path := "/api/v1/entities/" + url.PathEscape(id)
	var cur Entity
	if err := c.do(http.MethodGet, path, nil, &cur); err != nil {
		return err
	}
	merged := map[string]any{}
	for k, v := range cur.Attributes {
		merged[k] = v
	}
	for k, v := range attrs {
		merged[k] = v
	}
	return c.do(http.MethodPatch, path, map[string]any{"attributes": merged}, nil)
}
