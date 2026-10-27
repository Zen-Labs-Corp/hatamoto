package entity

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGenerateJSONLD_WebPage(t *testing.T) {
	schema := &EntitySchema{
		ID:   uuid.New(),
		Slug: "page",
		Name: "Page",
	}

	attrs := map[string]any{
		"description": "Test page description",
	}
	attrsJSON, _ := json.Marshal(attrs)

	entity := &Entity{
		ID:         uuid.New(),
		Name:       "Test Page",
		Slug:       "test-page",
		Attributes: attrsJSON,
		Schema:     schema,
	}

	jsonLD, err := GenerateJSONLD(entity, "https://example.com")
	if err != nil {
		t.Fatalf("GenerateJSONLD failed: %v", err)
	}

	if jsonLD["@type"] != "WebPage" {
		t.Errorf("Expected @type to be WebPage, got %v", jsonLD["@type"])
	}

	if jsonLD["name"] != "Test Page" {
		t.Errorf("Expected name to be 'Test Page', got %v", jsonLD["name"])
	}

	if jsonLD["url"] != "https://example.com/test-page" {
		t.Errorf("Expected url to be 'https://example.com/test-page', got %v", jsonLD["url"])
	}

	if jsonLD["description"] != "Test page description" {
		t.Errorf("Expected description to be 'Test page description', got %v", jsonLD["description"])
	}
}

func TestGenerateJSONLD_Article(t *testing.T) {
	schema := &EntitySchema{
		ID:   uuid.New(),
		Slug: "blog",
		Name: "Blog Post",
	}

	publishedAt := time.Date(2026, 2, 23, 10, 0, 0, 0, time.UTC)
	attrs := map[string]any{
		"excerpt":        "This is the excerpt",
		"author":         "John Doe",
		"featured_image": "https://example.com/image.jpg",
		"tags":           []any{"tech", "news"},
	}
	attrsJSON, _ := json.Marshal(attrs)

	entity := &Entity{
		ID:          uuid.New(),
		Name:        "Test Blog Post",
		Slug:        "test-blog-post",
		Attributes:  attrsJSON,
		Schema:      schema,
		PublishedAt: &publishedAt,
		UpdatedAt:   time.Now(),
	}

	jsonLD, err := GenerateJSONLD(entity, "https://example.com")
	if err != nil {
		t.Fatalf("GenerateJSONLD failed: %v", err)
	}

	if jsonLD["@type"] != "Article" {
		t.Errorf("Expected @type to be Article, got %v", jsonLD["@type"])
	}

	if jsonLD["headline"] != "Test Blog Post" {
		t.Errorf("Expected headline to be 'Test Blog Post', got %v", jsonLD["headline"])
	}

	if jsonLD["url"] != "https://example.com/blog/test-blog-post" {
		t.Errorf("Expected url to be 'https://example.com/blog/test-blog-post', got %v", jsonLD["url"])
	}

	author, ok := jsonLD["author"].(map[string]any)
	if !ok {
		t.Errorf("Expected author to be a map, got %T", jsonLD["author"])
	} else {
		if author["@type"] != "Person" {
			t.Errorf("Expected author @type to be Person, got %v", author["@type"])
		}
		if author["name"] != "John Doe" {
			t.Errorf("Expected author name to be 'John Doe', got %v", author["name"])
		}
	}

	if jsonLD["image"] != "https://example.com/image.jpg" {
		t.Errorf("Expected image to be 'https://example.com/image.jpg', got %v", jsonLD["image"])
	}

	keywords, ok := jsonLD["keywords"].([]string)
	if !ok {
		t.Errorf("Expected keywords to be []string, got %T", jsonLD["keywords"])
	} else if len(keywords) != 2 {
		t.Errorf("Expected 2 keywords, got %d", len(keywords))
	}
}

func TestGenerateJSONLD_Article_AuthorNameFallback(t *testing.T) {
	// The TeamMemberAuthorPicker writes author_name/author_entity_* and
	// never writes author. buildArticleJSONLD must still surface an author
	// in JSON-LD by falling back to author_name when author is empty.
	schema := &EntitySchema{
		ID:   uuid.New(),
		Slug: "blog",
		Name: "Blog Post",
	}

	attrs := map[string]any{
		"excerpt":     "This is the excerpt",
		"author_name": "Jane Roe",
	}
	attrsJSON, _ := json.Marshal(attrs)

	entity := &Entity{
		ID:         uuid.New(),
		Name:       "Test Blog Post",
		Slug:       "test-blog-post",
		Attributes: attrsJSON,
		Schema:     schema,
		UpdatedAt:  time.Now(),
	}

	jsonLD, err := GenerateJSONLD(entity, "https://example.com")
	if err != nil {
		t.Fatalf("GenerateJSONLD failed: %v", err)
	}

	author, ok := jsonLD["author"].(map[string]any)
	if !ok {
		t.Fatalf("Expected author to be a map, got %T", jsonLD["author"])
	}
	if author["@type"] != "Person" {
		t.Errorf("Expected author @type to be Person, got %v", author["@type"])
	}
	if author["name"] != "Jane Roe" {
		t.Errorf("Expected author name to be 'Jane Roe', got %v", author["name"])
	}
}

func TestGenerateJSONLD_Person(t *testing.T) {
	schema := &EntitySchema{
		ID:   uuid.New(),
		Slug: "team_member",
		Name: "Team Member",
	}

	attrs := map[string]any{
		"bio":       "Senior Software Engineer with 10 years experience.",
		"job_title": "Senior Software Engineer",
		"photo":     "https://example.com/photos/john.jpg",
		"email":     "john@example.com",
		"linkedin":  "https://linkedin.com/in/johndoe",
		"company":   "Example Corp",
		"skills":    []any{"Go", "TypeScript", "PostgreSQL"},
	}
	attrsJSON, _ := json.Marshal(attrs)

	entity := &Entity{
		ID:         uuid.New(),
		Name:       "John Doe",
		Slug:       "john-doe",
		Attributes: attrsJSON,
		Schema:     schema,
	}

	jsonLD, err := GenerateJSONLD(entity, "https://example.com")
	if err != nil {
		t.Fatalf("GenerateJSONLD failed: %v", err)
	}

	if jsonLD["@type"] != "Person" {
		t.Errorf("Expected @type to be Person, got %v", jsonLD["@type"])
	}

	if jsonLD["name"] != "John Doe" {
		t.Errorf("Expected name to be 'John Doe', got %v", jsonLD["name"])
	}

	if jsonLD["jobTitle"] != "Senior Software Engineer" {
		t.Errorf("Expected jobTitle to be 'Senior Software Engineer', got %v", jsonLD["jobTitle"])
	}

	if jsonLD["image"] != "https://example.com/photos/john.jpg" {
		t.Errorf("Expected image to be photo URL, got %v", jsonLD["image"])
	}

	if jsonLD["email"] != "john@example.com" {
		t.Errorf("Expected email to be 'john@example.com', got %v", jsonLD["email"])
	}

	sameAs, ok := jsonLD["sameAs"].([]string)
	if !ok {
		t.Errorf("Expected sameAs to be []string, got %T", jsonLD["sameAs"])
	} else if len(sameAs) != 1 || sameAs[0] != "https://linkedin.com/in/johndoe" {
		t.Errorf("Expected sameAs to contain LinkedIn URL, got %v", sameAs)
	}

	worksFor, ok := jsonLD["worksFor"].(map[string]any)
	if !ok {
		t.Errorf("Expected worksFor to be a map, got %T", jsonLD["worksFor"])
	} else {
		if worksFor["@type"] != "Organization" {
			t.Errorf("Expected worksFor @type to be Organization, got %v", worksFor["@type"])
		}
		if worksFor["name"] != "Example Corp" {
			t.Errorf("Expected worksFor name to be 'Example Corp', got %v", worksFor["name"])
		}
	}

	knowsAbout, ok := jsonLD["knowsAbout"].([]string)
	if !ok {
		t.Errorf("Expected knowsAbout to be []string, got %T", jsonLD["knowsAbout"])
	} else if len(knowsAbout) != 3 {
		t.Errorf("Expected 3 skills, got %d", len(knowsAbout))
	}
}

func TestGenerateJSONLD_NilEntity(t *testing.T) {
	_, err := GenerateJSONLD(nil, "https://example.com")
	if err == nil {
		t.Error("Expected error for nil entity")
	}
}

func TestGenerateJSONLD_NilSchema(t *testing.T) {
	entity := &Entity{
		ID:     uuid.New(),
		Name:   "Test",
		Schema: nil,
	}
	_, err := GenerateJSONLD(entity, "https://example.com")
	if err == nil {
		t.Error("Expected error for nil schema")
	}
}

func TestGenerateJSONLD_ValidatesJSONSyntax(t *testing.T) {
	// Test that the output is valid JSON
	schema := &EntitySchema{
		ID:   uuid.New(),
		Slug: "location",
		Name: "Location",
	}

	attrs := map[string]any{
		"address": map[string]any{
			"street":      "123 Collins St",
			"city":        "Melbourne",
			"state":       "VIC",
			"postal_code": "3000",
			"country":     "AU",
		},
		"phone": "+61 3 1234 5678",
	}
	attrsJSON, _ := json.Marshal(attrs)

	entity := &Entity{
		ID:         uuid.New(),
		Name:       "Melbourne Office",
		Slug:       "melbourne",
		Attributes: attrsJSON,
		Schema:     schema,
	}

	jsonLD, err := GenerateJSONLD(entity, "https://example.com")
	if err != nil {
		t.Fatalf("GenerateJSONLD failed: %v", err)
	}

	// Marshal to JSON and back to verify syntax
	jsonBytes, err := json.Marshal(jsonLD)
	if err != nil {
		t.Fatalf("Failed to marshal JSON-LD to JSON: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("Generated JSON-LD is not valid JSON: %v", err)
	}

	// Verify required fields for Google Rich Results Test
	if parsed["@context"] != "https://schema.org" {
		t.Errorf("Missing or invalid @context")
	}

	if parsed["@type"] == nil {
		t.Errorf("Missing @type")
	}
}
