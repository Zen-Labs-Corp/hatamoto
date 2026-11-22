package server

import (
	"testing"

	"hatamoto.dev/core/internal/render"
)

func TestRobotsTxtGolden(t *testing.T) {
	checkFeedGolden(t, "robots.txt", []byte(render.RobotsWithDeny("https://acme.example", nil, []string{"GPTBot", "CCBot"})))
}
