# Widget schemas

One directory per widget kind: `widgets/<kind>/schema.json`. The file is a JSON
Schema for the node's props plus an `x-hm` object with palette metadata
(`label`, `hint`, `group`, `order`, `children`, `seed`, `default`). Property
schemas may carry `x-hm-widget` (an inspector control hint). The view
(`widget.html`, `widget.css`, `island.js`) lives in `internal/render/widgets/<kind>/`
so this package stays a leaf: data only, no HTML, no sanitiser.

Array-typed props are refused by the loader; repeat structures with child nodes.
