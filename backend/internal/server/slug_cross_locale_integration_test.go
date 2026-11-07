//go:build integration

package server

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/internal/jsonx"
)

// A slug already used on the other side of the und / language-specific split
// is refused over REST with 409 slug_exists, on create (both orders) and on a
// PATCH that moves a row's slug into the clash.
func TestRESTSlugCrossLocale(t *testing.T) {
	e := newLocaleNeutralEnv(t, multiLocales)
	do := func(method, path string, body map[string]any) (int, []byte) {
		t.Helper()
		raw, _ := jsonx.Marshal(body)
		req, _ := http.NewRequest(method, e.ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("X-API-Key", e.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	create := func(slug, locale string) (int, []byte) {
		body := map[string]any{"schema_id": e.flaggedID, "name": "N " + slug, "slug": slug, "attributes": map[string]any{}}
		if locale != "" {
			body["locale"] = locale
		}
		return do(http.MethodPost, "/api/v1/entities", body)
	}
	wantConflict := func(what string, code int, raw []byte) {
		t.Helper()
		if code != http.StatusConflict {
			t.Fatalf("%s = %d, want 409: %s", what, code, raw)
		}
		var apiErr struct {
			Code string `json:"code"`
		}
		if err := jsonx.Unmarshal(raw, &apiErr); err != nil || apiErr.Code != "slug_exists" {
			t.Errorf("%s body = %s, want code slug_exists", what, raw)
		}
	}
	sfx := uuid.NewString()[:6]

	// en first, then locale-less (und) with the same slug.
	enSlug := "about-" + sfx
	if code, raw := create(enSlug, "en"); code != http.StatusCreated {
		t.Fatalf("POST en = %d: %s", code, raw)
	}
	code, raw := create(enSlug, "")
	wantConflict("POST und over en", code, raw)

	// und first, then en with the same slug.
	undSlug := "contact-" + sfx
	if code, raw := create(undSlug, ""); code != http.StatusCreated {
		t.Fatalf("POST und = %d: %s", code, raw)
	}
	code, raw = create(undSlug, "en")
	wantConflict("POST en over und", code, raw)

	// Two language-specific rows may still share a slug.
	if code, raw := create(enSlug, "fr"); code != http.StatusCreated {
		t.Fatalf("POST fr same slug as en = %d: %s", code, raw)
	}

	// PATCH a fresh en row's slug into the und row's slug.
	code, raw = create("moving-"+sfx, "en")
	if code != http.StatusCreated {
		t.Fatalf("POST moving en = %d: %s", code, raw)
	}
	var moving struct {
		ID uuid.UUID `json:"id"`
	}
	if err := jsonx.Unmarshal(raw, &moving); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	code, raw = do(http.MethodPatch, "/api/v1/entities/"+moving.ID.String(), map[string]any{"slug": undSlug})
	wantConflict("PATCH en slug into und slug", code, raw)
}

// The MCP create tool surfaces the same refusal as a tool error with the
// domain text.
func TestMCPSlugCrossLocale(t *testing.T) {
	e := newLocaleNeutralEnv(t, multiLocales)
	c := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "1"}, nil)
	sess, err := c.Connect(e.ctx, &mcp.StreamableClientTransport{
		Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{e.key}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	create := func(slug, locale string) (bool, string) {
		t.Helper()
		args := map[string]any{"schema": e.flaggedSlug, "name": "N " + slug, "slug": slug, "attributes": map[string]any{}}
		if locale != "" {
			args["locale"] = locale
		}
		res, err := sess.CallTool(e.ctx, &mcp.CallToolParams{Name: "entities_create", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res.IsError, res.Content[0].(*mcp.TextContent).Text
	}
	slug := "about-" + uuid.NewString()[:6]
	if isErr, text := create(slug, "en"); isErr {
		t.Fatalf("create en: %s", text)
	}
	isErr, text := create(slug, "")
	if !isErr || !strings.Contains(text, "slug already exists") {
		t.Fatalf("create und over en: isError=%v text=%q, want a slug-already-exists tool error", isErr, text)
	}
}
