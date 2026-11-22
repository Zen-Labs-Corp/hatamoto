package server

import (
	"testing"

	"hatamoto.dev/core/internal/render"
)

func TestLLMsTxtGolden(t *testing.T) {
	counts := map[string]int{"page": 3, "post": 12, "case-study": 1}
	checkFeedGolden(t, "llms.txt",
		[]byte(buildLLMsTxt("Acme", "https://acme.example", "2026-10-13", 16, []string{"page", "post", "case-study"}, counts)))
	// No posts: no feed bullet. No content at all: no Content Types section.
	checkFeedGolden(t, "llms-empty.txt", []byte(buildLLMsTxt("Knowledge Base", "https://acme.example", "2026-10-13", 0, nil, map[string]int{})))
}

func TestRobotsTxtGolden(t *testing.T) {
	checkFeedGolden(t, "robots.txt", []byte(render.RobotsWithDeny("https://acme.example", nil, []string{"GPTBot", "CCBot"})))
}
