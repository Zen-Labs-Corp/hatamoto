package app

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// getPort is the port `serve` listens on (API_PORT, default 8090).
func getPort() string {
	if p := os.Getenv("API_PORT"); p != "" {
		return p
	}
	return "8090"
}

// healthcheck GETs url and fails unless it answers 200. It lets the image
// carry a HEALTHCHECK without wget or curl.
func healthcheck(c *http.Client, url string) error {
	cl := *c
	cl.Timeout = 4 * time.Second
	resp, err := cl.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return nil
}
