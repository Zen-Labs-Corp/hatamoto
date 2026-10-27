# WordPress fixture

A seeded WordPress the importer tests run against. Two ways to start it, one seed script.

| | Docker (CI) | No Docker (local) |
|---|---|---|
| start | `./docker-up.sh` | `./run-local.sh up` |
| stack | `wordpress:7.1.3` + `mariadb:11.4` + `wordpress:cli-2.12.0` | static PHP 8.4 CLI (`php -S`) + SQLite Database Integration 3.1.0 |
| site URL | `http://127.0.0.1:8089` | a free local port, printed |
| root needed | docker group | no |

Both print `WP_FIXTURE_URL`, `WP_FIXTURE_USER`, `WP_FIXTURE_APP_PASSWORD` and `WP_FIXTURE_WXR`
(a WXR export of the same site). Every download is pinned by version and sha256 (local) or
tag (docker): WordPress 7.1.3, Yoast SEO 28.7, Contact Form 7 6.2.2.

`seed.sh` (wp-cli, run by both) creates: title, tagline, `/%year%/%monthnum%/%postname%/`
permalinks; pages `services > seo > local`, Home (front page), Blog (posts page), About,
Contact (Contact Form 7 shortcode) and an Elementor-marked landing page; posts with a
hierarchical category (`News > Product Updates`) and tags, dated permalinks, a gallery block,
YouTube and Vimeo embeds, an unsupported shortcode, a private (members-only) post, a draft and a
`noindex` post; Yoast titles, descriptions and a canonical on some of them; an author with a bio
and a local avatar; a `portfolio` custom post type; two menus; six images (one uploaded twice
under another name); an application password for the REST API.

## Run the tests

```sh
test/wpfixture/run-local.sh up                       # or ./docker-up.sh
set -a; . ~/.cache/hatamoto-wpfixture/site/wpfixture.env; set +a   # docker: out/wpfixture.env.host
cd backend
DATABASE_URL=postgres://... go test -tags=integration -run WordPress ./app
test/wpfixture/run-local.sh down                     # keep data; `reset` deletes the site
```

`run-local.sh wp <args>` runs wp-cli against the local site.

## The images

The six files in `assets/` are generated colour-bar images (vertical bars), not photographs, so the repository carries no third-party artwork and no
licence terms. `gallery-1.jpg`, `gallery-2.jpg`, `hero.jpg` and `hero-copy.jpg` are 1200x800 JPEG;
`avatar.png` and `gallery-3.png` are 400x400 PNG. `hero-copy.jpg` is a byte-for-byte copy of
`hero.jpg`: the seed uploads it under another name to test duplicate-media detection, so copy
`hero.jpg` again rather than generating a second file. The seed picks files by name
(`seed.sh`, `assets/$f.*`).

To regenerate one (the tests count images and expect exactly one byte-identical pair, `hero.jpg` and `hero-copy.jpg`, so give every other file a different colour; they do not compare pixels), save this as
`main.go` in an empty directory with a `go.mod`, then run `go run main.go OUT W H R G B`, for example
`go run main.go gallery-1.jpg 1200 800 100 90 160`. A `.png` name writes a PNG.

```go
package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
)

// usage: go run main.go out.jpg|out.png WIDTH HEIGHT R G B
func main() {
	var w, h, r, g, b int
	for i, p := range []*int{&w, &h, &r, &g, &b} {
		for _, c := range os.Args[2+i] {
			*p = *p*10 + int(c-'0')
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		bar := x * 4 / w // four vertical bars, each a little lighter than the last
		c := color.RGBA{uint8(r + 20*bar), uint8(g + 10*bar), uint8(b), 255}
		for y := 0; y < h; y++ {
			img.Set(x, y, c)
		}
	}
	f, _ := os.Create(os.Args[1])
	defer f.Close()
	if strings.HasSuffix(os.Args[1], ".png") {
		png.Encode(f, img)
	} else {
		jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
	}
}
```
