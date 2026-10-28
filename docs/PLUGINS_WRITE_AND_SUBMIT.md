# Write and submit a plugin

How to write a Hatamoto plugin in your own GitHub repo and get it approved for
the registry. Part 1 covers writing and testing, Part 2 covers the submission.
The full reference (hooks, payloads, host functions, permissions) is
[PLUGIN_API.md](PLUGIN_API.md); packaging and trust are in
[PACKAGES.md](PACKAGES.md).

> **Status:** the Hatamoto SDK (`hatamoto.dev/sdk`) is fetchable with `go get`
> only once Hatamoto core goes public (roadmap item v1-24). Submissions to the
> registry open at the same time. Until then the steps below describe the path
> you will follow, and a plugin inside this repository uses the `replace`
> described in PLUGIN_API.md.

Approved plugins are listed at <https://hatamoto.dev/plugins> and install with
one click from Admin > Plugins > Browse registry. Looking to publish a theme
instead? See [THEMES_WRITE_AND_SUBMIT.md](THEMES_WRITE_AND_SUBMIT.md).

## Part 1: Write a plugin

A plugin is a WebAssembly module built with the standard Go toolchain, plus a
`plugin.json` that declares what it needs. It lives in your own repo.

> A package that only adds page-builder widgets needs no WebAssembly at all, and a plugin can feed live data to
> the builder: see [WIDGET_PACKS.md](WIDGET_PACKS.md).

### Prerequisites

- Go 1.26 or newer.
- The `hatamoto` binary, for `keys generate` and `plugin pack`.
- A public GitHub repo for your plugin.

### 1. Create the module

```sh
mkdir hatamoto-acme-seo && cd hatamoto-acme-seo
git init
go mod init github.com/acme/hatamoto-acme-seo
go get hatamoto.dev/sdk@v0.12.0
```

SDK 0.12.0 ships with Hatamoto `v0.31.0`; until your users run it, declare `^0.11`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown.

Your `go.mod` must resolve the SDK from the network. Do **not** add a `replace`
line for `hatamoto.dev/sdk` (to any target), no local-path `replace` and no
`go.work` file; the registry build runs in a clean checkout and refuses a plugin
that has any of them. It also builds with the Hatamoto team's pinned Go toolchain
and never downloads a newer one, so a plugin whose `go.mod` requires a newer Go than
that toolchain (currently go1.27.3) fails to build. After `go mod tidy` it looks like this:

```
module github.com/acme/hatamoto-acme-seo

go 1.26

require hatamoto.dev/sdk v0.12.0

require github.com/extism/go-pdk v1.1.3 // indirect
```

### 2. Lay out the files

Copy the split used by the first-party [`gtm` plugin](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/gtm). It keeps the code that only
makes sense inside WebAssembly apart from the code you can unit test on your
machine:

```
hatamoto-acme-seo/
  go.mod
  go.sum
  plugin.json         what the plugin is and needs
  plugin.go           your logic (all platforms)
  host.go             the host call seam (all platforms)
  host_wasip1.go      //go:build wasip1: wires the seam to the real host
  exports_wasip1.go   //go:build wasip1: the //go:wasmexport lines
  main_other.go       //go:build !wasip1: an empty main so tests and vet run
  plugin_test.go      host tests
  assets/             optional: plugin.js / plugin.css linked on every public page
  migrations/         optional: SQL for your own tables
  ui/                 optional: admin UI files
```

`host.go`: the seam your logic calls instead of calling the SDK directly.

```go
package main

// callHost is the host API seam: guest.Call inside the wasm module, a fake in tests.
var callHost func(fn string, args, out any) error
```

`host_wasip1.go`:

```go
//go:build wasip1

package main

import "hatamoto.dev/sdk/guest"

func init() { callHost = guest.Call }
```

`main_other.go`:

```go
//go:build !wasip1

package main

// main exists so the package vets and tests on the host; the plugin itself is
// only ever built for wasip1.
func main() {}
```

`exports_wasip1.go`: one `//go:wasmexport` line per hook you handle. The
directive only works on a function in `package main`.

```go
//go:build wasip1

package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

//go:wasmexport filter_render_head
func filterRenderHead() int32 { return guest.Run(sdk.RenderHead.Export()) }

// main is never called: a c-shared wasm module is a reactor.
func main() {}
```

`plugin.go`: register handlers in `init`. This one adds a meta tag to every
page head and reads a setting from the host.

```go
package main

import (
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnFilter(sdk.RenderHead, addTag)
}

// addTag never fails the render: a missing setting just means no tag.
func addTag(c sdk.HeadContext) (sdk.HeadContext, error) {
	var s struct {
		SiteName string `json:"site_name"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return c, nil
	}
	name := strings.TrimSpace(s.SiteName)
	if name == "" {
		return c, nil
	}
	c.Tags = append(c.Tags, sdk.HeadTag{
		Kind:  "meta",
		Attrs: map[string]string{"name": "acme-site", "content": name},
	})
	return c, nil
}
```

The `hello` plugin (`examples/plugins/hello/main.go`) shows the
other hook kind: an action (`guest.OnAction(sdk.EntityPublished, ...)`) next to
a filter. If you only need one file, put the logic, the `//go:wasmexport` lines
and an empty `func main() {}` in `main.go`, as `hello` does; you then cannot unit
test on the host without building for wasip1, which is why the split is
recommended.

### 3. Write plugin.json

```json
{
  "name": "acme-seo",
  "version": "0.1.0",
  "sdk": "^0.11",
  "description": "One sentence a site owner will read in the plugin directory.",
  "permissions": ["settings"],
  "limits": { "memory_pages": 256, "max_instances": 4 },
  "settings_schema": {
    "type": "object",
    "properties": {
      "site_name": { "type": "string", "title": "Site name", "maxLength": 80 }
    }
  }
}
```

| Field | Meaning |
|---|---|
| `name` | `^[a-z][a-z0-9-]{1,39}$`; also the name of your manifest file here |
| `version` | your plugin's semver; bump it for every release |
| `sdk` | SDK range you built against, for example `^0.11` (declare `^0.11` until your users run v0.31.0, then `^0.12`) |
| `description` | shown in the plugin directory |
| `permissions` | capabilities you ask for (`settings`, `kv`, `db`, `http.fetch`, ...). Ask for the least you need; reviewers check each one |
| `allowed_hosts` | the hosts `http.fetch` may call; list them exactly |
| `routes`, `cron`, `funcs`, `cookies`, `limits`, `settings_schema` | optional, see the plugin reference |

Site owners grant permissions at install time, and a plugin only ever gets what
they grant.

### 4. Build

```sh
rm -rf build
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
cp plugin.json build/plugin.json
for d in migrations ui assets; do if [ -d "$d" ]; then cp -r "$d" "build/$d"; fi; done
```

`build/` is exactly what gets packed. This is also the recipe the registry
uses, so if it works here it works there. Add `build/` to `.gitignore`.

Only Go plugins can be listed today. A plugin written in another language can
be packed and installed on your own server, but the registry builder runs the Go
recipe above and does not build other toolchains.

### 5. Test on your machine

Because of the `!wasip1` split, `go test` runs natively and no WebAssembly
runtime is needed. `guest.Handle` runs a registered handler without the PDK and
`callHost` is replaced with a fake:

```go
package main

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func TestAddsTag(t *testing.T) {
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		return json.Unmarshal([]byte(`{"site_name":"Acme"}`), out)
	}
	in, _ := json.Marshal(sdk.HeadContext{Path: "/"})
	out, err := guest.Handle(sdk.RenderHead.Export(), in)
	if err != nil {
		t.Fatal(err)
	}
	var c sdk.HeadContext
	if err := json.Unmarshal(out, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Tags) != 1 || c.Tags[0].Attrs["content"] != "Acme" {
		t.Fatalf("tags = %+v", c.Tags)
	}
}
```

```sh
go vet ./... && go test ./...
```

Test the failure paths too: a plugin should fail open (return its input
unchanged) rather than break a page render.

### 6. Pack and try it on a server

Make a signing key for yourself and pack the build folder:

```sh
hatamoto keys generate --name mykey      # mykey.key (secret) + mykey.pub
hatamoto plugin pack ./build --key mykey.key --out acme-seo.hmp
```

Keep `mykey.key` out of git. This key is only for your own trials: the registry
re-builds your plugin from source and signs it with the Hatamoto key, so your
signature is never shipped.

To try the package on a development server, pick one:

- **Trust your key.** Put the base64 public key that `keys generate` prints into
  `HATAMOTO_TRUSTED_KEYS` on the server (several keys may be separated by commas
  or spaces) and restart. Then install the package:
  `hatamoto package install acme-seo.hmp --grant settings`.
- **Allow unsigned packages.** An unsigned package (pack without `--key`) installs
  only after the owner switches on "Allow unsigned packages" in Admin > Settings >
  Plugins. It is off by default and needs an acknowledgement tick; an unsigned
  package can run any code its author wrote, so use it only for your own code.
- **Dev override.** `HATAMOTO_PLUGIN_DIR=./plugins` loads
  `./plugins/<name>/plugin.json` and `plugin.wasm` as an unsigned in-memory
  package, replacing an installed one of the same name. Nothing is stored and a
  restart reloads it. It is for development only, never for deployment.

### 7. Publish your repo and submit

Push your code to GitHub, tag a release if you like, copy the 40 character commit
id you want reviewed, and follow [Part 2](#part-2-submit-a-plugin).

## Part 2: Submit a plugin

Submissions go to the public plugins repository <https://github.com/Zen-Labs-Corp/hatamoto-plugins>.

### How it works

1. You keep your plugin in your own public GitHub repo.
2. You open a pull request here that adds one small manifest pinning a commit.
3. CI checks the manifest. A maintainer reviews your code at that commit.
4. A merge is the approval. After the merge, CI keeps a copy of the pinned
   commit at `github.com/Zen-Labs-Corp/<name>`. The release manager
   builds from that copy, signs the result with the Hatamoto key and publishes
   it. Every signed build is kept.

The release build downloads and verifies your modules through `go.sum`, exactly
like CI. A `vendor/` directory, in your repo or in our copy, is ignored.

We never accept binaries. Everything is built from the source you pinned. By
submitting you grant the project the right to archive, build, sign and
redistribute that source and its builds, even if you later delete or change
your repository.

### The manifest

Add `manifests/<name>.json`, where `<name>` is the `name` in your `plugin.json`:

```json
{
  "name": "acme-seo",
  "repo": "https://github.com/acme/hatamoto-acme-seo",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "subdir": "",
  "title": "Acme SEO Helper",
  "author": "acme",
  "license": "MIT",
  "archive_grant": true,
  "sdk": "^0.11"
}
```

| Field | Required | Rule |
|---|---|---|
| `name` | yes | `^[a-z][a-z0-9-]{1,39}$`; equals the file name and the `name` in `plugin.json` |
| `repo` | yes | `https://github.com/<owner>/<repo>` |
| `commit` | yes | full 40 character lowercase hex commit id |
| `subdir` | no | plugin folder inside the repo; no `..`, no leading `/` |
| `title` | no | display name for the directory card, 1 to 60 characters, no control characters, no leading or trailing space; without it the card shows `name` |
| `author` | yes | your GitHub name, shown as "by @author" |
| `license` | yes | one of: `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `MPL-2.0`, `GPL-2.0`, `GPL-3.0`, `LGPL-2.1`, `LGPL-3.0`, `AGPL-3.0` (the GPL, LGPL and AGPL ids also with `-only` or `-or-later`) |
| `archive_grant` | yes | must be `true`: you grant us the right to keep a copy of the source at the pinned commit and its builds (see above) |
| `sdk` | yes | SDK range you built against; matches `plugin.json` |
| `homepage` | no | an `https://` link |

`tier` is reserved for the maintainers: `first-party` is accepted only for repositories under
the `Zen-Labs-Corp` organization (such a manifest has no `author`), and is refused for everyone
else. Leave it out; your plugin is then listed as Community.

No other keys are allowed. See `manifests/schema.json` in the plugins repository.
Check your file before you push:

```sh
scripts/validate-manifest.sh manifests/acme-seo.json --remote   # run in a clone of hatamoto-plugins
```

`--remote` also fetches your repo and confirms the commit exists and that
`plugin.json` at that commit has the same name.

### The pull request

Fork this repo, add your manifest, and open a PR. Fill in the template: it asks
you to justify each permission. One plugin per PR.

CI runs the validator on your manifest and, once the SDK is public, builds your
plugin at the pinned commit with
`GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared`.

### Review criteria

A maintainer reads the source at the pinned commit. We look for:

- **Pinned source.** The commit exists in the public repo and is what you want
  shipped. Branches and tags are not accepted, only commit ids.
- **Reproducible build.** It builds from a clean checkout with the recipe in
  Part 1 of this guide, no `replace` for the SDK, no `go.work`, no `go generate`, no network use at build
  time beyond fetching Go modules.
- **Justified permissions.** Each entry in `permissions` is needed for a
  feature the description promises.
- **Minimal `allowed_hosts`.** Exact hosts, not `*`, unless the plugin must call
  URLs the owner configures (and then say so in the PR).
- **Readable code.** No obfuscated, minified or encoded blobs, no hidden
  behaviour, no data sent anywhere the description does not mention.
- **License.** The repo has a license file, and it matches the manifest.
- **Fit.** The plugin works, has tests, and does not duplicate a first-party
  plugin without good reason.

Reviewers may ask for changes. Push fixes to your repo and update `commit` in
the PR.

### After the merge

The release manager builds your plugin from the pinned commit, signs it with the
Hatamoto key and publishes a new registry index. It then shows up in Browse
registry on every Hatamoto server and as a card on
<https://hatamoto.dev/plugins>, labelled "Community" and "by @author". Publishing
is done by a person with the signing key, so it can lag the merge by a little.

### Updating a plugin

Open a new PR that changes `commit` (and `sdk` if it changed) in your existing
manifest. Bump `version` in `plugin.json` first, so servers can tell the new build from
the old one. The review looks at the
changes since the previous commit.

### Removing a plugin

Open a PR that deletes your manifest. After it merges the release manager
publishes an index without the plugin. Copies already installed keep working until
each site owner removes them. If a plugin is found to be harmful, maintainers may
remove its manifest without notice and publish immediately.
