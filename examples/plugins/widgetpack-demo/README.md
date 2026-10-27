# widgetpack-demo - reference widget pack

A plugin package with no code: `plugin.json` plus `widgets/`. It adds two
widgets to the page builder under **Plugin widgets**:

| Kind | Files | Shows |
|---|---|---|
| `widgetpack-demo_promo` | `schema.json`, `widget.html`, `widget.css`, `island.js` | a banner with a bindable title, text and link; the island adds a Dismiss button |
| `widgetpack-demo_pricing` | `schema.json`, `widget.html`, `widget.css` | a pricing card with bindable plan and price, and child nodes |

The kind is `<plugin>_<widget>`, so a pack can never shadow a core widget.
Every file is validated when the package is installed (template actions, markup,
CSS scoping to `.hm-<kind>`, island limits); see `docs/WIDGET_PACKS.md`.

## Pack and install

```sh
hatamoto plugin pack examples/plugins/widgetpack-demo --out widgetpack-demo.hmp
hatamoto package install widgetpack-demo.hmp --source dev --dev --confirm --grant builder.island
```

The promo widget ships an `island.js`, so the package lists the `builder.island`
permission ("runs JavaScript on your site's pages"). Without `--grant` the
widgets still render; the Dismiss button script is not served.

Widgets appear in the builder at once (no restart). Uninstalling the package
leaves saved pages intact: the builder shows a "not installed" notice in place
of each widget and the public page prints a comment; reinstalling restores them.

Pair it with `datasource-demo` to bind the banner's title to
`ext.datasource-demo.temp`.
