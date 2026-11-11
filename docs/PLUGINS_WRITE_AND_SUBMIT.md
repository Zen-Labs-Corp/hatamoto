# Write and submit a plugin

How to write a Hatamoto plugin in your own GitHub repo and publish it through a
developer account. Part 1 covers writing and testing, Part 2 covers publishing.
The full reference (hooks, payloads, host functions, permissions) is
[PLUGIN_API.md](PLUGIN_API.md); packaging and trust are in
[PACKAGES.md](PACKAGES.md).

> **Status:** the Hatamoto SDK (`hatamoto.dev/sdk`) is fetchable with `go get`
> only once Hatamoto core goes public (roadmap item v1-24). Until then the steps
> below describe the path you will follow, and a plugin inside this repository
> uses the `replace` described in PLUGIN_API.md.

Plugins published this way are listed as Developer packages in Admin > Plugins >
Browse registry. Looking to publish a theme instead? See
[THEMES_WRITE_AND_SUBMIT.md](THEMES_WRITE_AND_SUBMIT.md).

## Part 1: Write a plugin

A plugin is a WebAssembly module built with the standard Go toolchain, plus a
`plugin.json` that declares what it needs. It lives in your own repo.

> A package that only adds page-builder widgets needs no WebAssembly at all, and a plugin can feed live data to
> the builder: see [WIDGET_PACKS.md](WIDGET_PACKS.md).

### Prerequisites

- Go 1.27 or newer.
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
that toolchain (currently go1.27.4) fails to build. After `go mod tidy` it looks like this:

```
module github.com/acme/hatamoto-acme-seo

go 1.27

require hatamoto.dev/sdk v0.20.0

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

### 7. Publish your repo and release

Push your code to GitHub, build a signed `.hmp` file, attach it to a GitHub
release, and follow [Part 2](#part-2-publish-your-plugin).

## Part 2: Publish your plugin

Anyone can publish. Developer accounts are free for now. We may introduce a fee later and will announce it in advance. You need an account to submit.

1. **Create an account.** Open <https://hatamoto.dev/developers>, choose
   "Sign in with GitHub", add a display name and accept the developer terms.
2. **Make a signing key.** Run `hatamoto keys generate --name mykey`. Keep
   `mykey.key` private. On the dashboard, paste the contents of `mykey.pub`
   and choose "Add key". Each account has one key.
3. **Pack and sign.** Run `hatamoto plugin pack . --key mykey.key`, then attach
   the `.hmp` file to a release on GitHub.
4. **Submit.** On the dashboard fill in the form and choose "Check and submit":
   - Kind: Plugin.
   - Release file: the link to the `.hmp` file, in the form
     `https://github.com/<owner>/<repo>/releases/download/<tag>/<file>.hmp`.
     Other hosts and release page links are refused.
   - Source repository (optional), Title (optional).
   - What it does (up to 600 characters) and Category.

The checks run at once and each result is shown in a list. If one fails, nothing
is submitted and the list says why. If all pass, the status is "Waiting for
approval". The site owner approves the plugin or tells you why not, by email.
We give no fixed review time.

**Names.** A name is lower case letters, digits and hyphens, matching
`^[a-z][a-z0-9-]{1,39}$`. It becomes yours when your first version is approved.

**After approval.** The plugin appears as a Developer package. Hatamoto never
signs your code, and it checks who signed a package but does not review the
code. A site owner must first turn on Admin > Plugins > Package security >
Allow developer packages. That takes three steps: an acknowledgement, typing
the site address, and the owner password. Then anyone on that site can install
it from Browse registry or with `hatamoto package install <name>`.

**Updating.** Publish a new release with a higher version, signed by the same
key, and submit it again. After a rejection, use "Resubmit".

**Removing.** To take a plugin down, contact us. We may refuse or remove
listings as the developer terms allow. A listing also drops out by itself if
its file or signer changes.

## First-party listing

This section is for first-party plugins and for community plugins already in the
main registry. Outside authors publish through a developer account, as described
in [Part 2](#part-2-publish-your-plugin).

The public repository <https://github.com/Zen-Labs-Corp/hatamoto-plugins> lists
these plugins. Each one is a small manifest that pins a commit in the author's
own repo. After a maintainer reviews the code at that commit and merges the
manifest, the release manager builds the plugin from the pinned source, signs it
with the Hatamoto key and publishes it in the registry index. Binaries are never
accepted, and a `vendor/` directory is ignored. By listing, you grant the
project the right to archive, build, sign and redistribute that source and its
builds.

### Manifest

The manifest is `manifests/<name>.json`, where `<name>` is the `name` in `plugin.json`:

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
| `title` | no | display name for the directory card, 1 to 40 characters, no control characters, no leading or trailing space, plus the title rules below; without it the card shows `name` |
| `author` | yes | your GitHub name, shown as "by @author" |
| `license` | yes | one of: `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `MPL-2.0`, `GPL-2.0`, `GPL-3.0`, `LGPL-2.1`, `LGPL-3.0`, `AGPL-3.0` (the GPL, LGPL and AGPL ids also with `-only` or `-or-later`) |
| `archive_grant` | yes | must be `true`: you grant us the right to keep a copy of the source at the pinned commit and its builds (see above) |
| `sdk` | yes | SDK range you built against; matches `plugin.json` |
| `homepage` | no | an `https://` link |

`tier: first-party` is accepted only for repositories under the `Zen-Labs-Corp`
organization, and such a manifest has no `author`. Other manifests are listed as
Community. The schema is `manifests/schema.json` in the plugins repository, and
`scripts/validate-manifest.sh manifests/<name>.json --remote` checks a manifest
and that the commit exists.

### Listing rules

The validator and the registry build both enforce these rules. The word lists
live in the `policy/` folder of the plugins repository.

- `author` must equal the owner of `repo`, except for first-party plugins.
- No part of the name may be a banned word or a reserved name, or a brand you do
  not own, or one character away from another listed name.
- The title has at most 40 characters, one script, and shares a word with the
  name.
- The description in `plugin.json` has 20 to 160 characters, no URL, no emoji
  and no banned words.

### Review criteria

A maintainer reads the source at the pinned commit and looks for:

- A commit id (not a branch or tag) that builds from a clean checkout with the
  Part 1 recipe, with no SDK `replace`, no `go.work` and no `go generate`.
- Permissions and `allowed_hosts` that match what the description promises.
- Readable code, with no hidden behaviour and no undisclosed data sent out.
- A license file that matches the manifest.
- One listing per product, no listings that only point elsewhere, and a paid
  external service stated in the description.

### Updating and removing

To update, open a pull request that changes `commit` (and `sdk` if it changed),
after bumping `version` in `plugin.json`. To remove a plugin, open a pull request
that deletes its manifest. Installed copies keep working until each site owner
removes them. Maintainers may remove a harmful plugin without notice.
