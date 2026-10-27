// Mounts the rich-text and html editors (js/editor/*, loaded on demand) on any
// screen whose markup carries data-hm-editor: schema forms, theme and plugin
// settings. The builder and the blog editor mount their own editors.
import { enhanceEditors } from "@hm/editor/mount";

document.addEventListener("DOMContentLoaded", () => enhanceEditors(document));
document.addEventListener("htmx:afterSettle", (e) => enhanceEditors(e.target));
