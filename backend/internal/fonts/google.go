package fonts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	cssHost      = "fonts.googleapis.com"
	fileHost     = "fonts.gstatic.com"
	maxCSSBytes  = 256 << 10
	downloadTime = 20 * time.Second

	// A current desktop browser, so Google answers with woff2 files.
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

// Google downloads fonts once from Google Fonts. Visitors never contact it.
type Google struct {
	Client *http.Client
	CSSURL string
	// AllowHost says whether host may be contacted; file is true for a font
	// file and false for the stylesheet. Redirects are checked the same way.
	AllowHost func(host string, file bool) bool
}

// NewGoogle returns a downloader that talks to the real Google hosts through
// client, which should be a safefetch client.
func NewGoogle(client *http.Client) *Google {
	return &Google{
		Client: client,
		CSSURL: "https://" + cssHost + "/css2",
		AllowHost: func(host string, file bool) bool {
			if file {
				return host == fileHost
			}
			return host == cssHost
		},
	}
}

// Download fetches the stylesheet for family at weights, then each font file,
// and returns the faces ready to store. With latinOnly it keeps the latin and
// latin-ext subsets only.
func (g *Google) Download(ctx context.Context, family string, weights []int, italic, latinOnly bool) ([]NewFont, error) {
	if !ValidFamily(family) {
		return nil, ErrBadFamily
	}
	query, err := cssQuery(family, weights, italic)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTime)
	defer cancel()
	client := g.client()

	sheet, err := g.get(ctx, client, g.CSSURL+"?"+query, false, maxCSSBytes)
	if err != nil {
		return nil, fmt.Errorf("Google Fonts could not be reached or does not know %q: %w", family, err)
	}
	faces, err := ParseFaces(string(sheet))
	if err != nil {
		return nil, err
	}
	var out []NewFont
	byURL := map[string][]byte{}
	for _, f := range faces {
		if !strings.EqualFold(f.Family, family) {
			return nil, sheetErr("family differs from the one asked for")
		}
		if latinOnly && f.Subset != "latin" && f.Subset != "latin-ext" {
			continue
		}
		data, ok := byURL[f.URL]
		if !ok {
			if data, err = g.get(ctx, client, f.URL, true, MaxFileBytes); err != nil {
				return nil, fmt.Errorf("downloading a font file: %w", err)
			}
			if !bytes.HasPrefix(data, []byte("wOF2")) {
				return nil, ErrNotWOFF2
			}
			byURL[f.URL] = data
		}
		out = append(out, NewFont{
			Family: family, Weight: f.Weight, Style: f.Style, Source: "google",
			Subset: f.Subset, UnicodeRange: f.UnicodeRange, Hash: hashOf(data), Data: data,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("Google Fonts has no Latin version of that font; untick Latin only to download it")
	}
	return out, nil
}

// client copies the configured client with a redirect check on top of its own.
func (g *Google) client() *http.Client {
	c := *g.Client
	prev := c.CheckRedirect
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if !g.AllowHost(r.URL.Hostname(), false) && !g.AllowHost(r.URL.Hostname(), true) {
			return fmt.Errorf("redirect to %q is not allowed", r.URL.Hostname())
		}
		if prev != nil {
			return prev(r, via)
		}
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return &c
}

func (g *Google) get(ctx context.Context, c *http.Client, rawURL string, file bool, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !g.AllowHost(u.Hostname(), file) {
		return nil, fmt.Errorf("host %q is not allowed", hostOf(u))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// A redirect hop was allowed by CheckRedirect, but the final host must
	// still be one for this kind of request.
	if !g.AllowHost(resp.Request.URL.Hostname(), file) {
		return nil, fmt.Errorf("host %q is not allowed", resp.Request.URL.Hostname())
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		if file {
			return nil, ErrTooBig
		}
		return nil, sheetErr("too large")
	}
	return b, nil
}

func hostOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Hostname()
}

// cssQuery builds the css2 query: family=Inter:ital,wght@0,400;1,400 with the
// weights sorted, or family=Inter:wght@400;500 without italics.
func cssQuery(family string, weights []int, italic bool) (string, error) {
	ws := append([]int(nil), weights...)
	sort.Ints(ws)
	var parts []string
	for i, w := range ws {
		if !ValidWeight(w) {
			return "", ErrBadWeight
		}
		if i > 0 && w == ws[i-1] {
			continue
		}
		if italic {
			parts = append(parts, "0,"+strconv.Itoa(w))
		} else {
			parts = append(parts, strconv.Itoa(w))
		}
	}
	if len(parts) == 0 {
		return "", errors.New("Choose at least one weight")
	}
	spec := "wght@" + strings.Join(parts, ";")
	if italic {
		var it []string
		for _, p := range parts {
			it = append(it, "1,"+strings.TrimPrefix(p, "0,"))
		}
		spec = "ital,wght@" + strings.Join(append(parts, it...), ";")
	}
	return "family=" + url.QueryEscape(family) + ":" + spec + "&display=swap", nil
}
