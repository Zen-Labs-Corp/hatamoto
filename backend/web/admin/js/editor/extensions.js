// The shared Tiptap extension factory (port of editor/extensions.ts): the single
// source of truth for the extension list, so every editor (page rich text, html
// Fields regions, v1-13's blog editor) builds the same structure. StarterKit
// runs with link:false so its bundled Link does not collide with EntityLink
// (both are named "link"): exactly one link mark is registered.
import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import Placeholder from "@tiptap/extension-placeholder";
import { Table } from "@tiptap/extension-table";
import { TableRow } from "@tiptap/extension-table-row";
import { TableHeader } from "@tiptap/extension-table-header";
import { TableCell } from "@tiptap/extension-table-cell";
import { EntityLink } from "@hm/editor/entitylink";

/**
 * opts: {placeholder, linkClass, imageClass, siteHosts, entityLinkClass, siteRel,
 *        prose, document, image}.
 * prose: the subset the html Fields gate accepts (no underline, inline code, code
 * block, image or table), so the editor cannot produce what Fields cannot
 * re-open. document: replaces StarterKit's Document node (a one-paragraph doc).
 * image: replaces the Image extension (the blog editor's captioned image).
 * marks (with prose): extra inline marks to keep, "underline" and/or "code" (a heading or text node's
 * inline editor). hardBreak: false removes the line break (a heading is one line).
 */
export function createEditorExtensions(opts = {}) {
  const {
    placeholder = "Start typing...",
    linkClass = null,
    imageClass = null,
    siteHosts = [],
    entityLinkClass = "entity-link",
    siteRel = true,
    prose = false,
    marks = [],
    hardBreak = true,
    document,
    image = Image,
  } = opts;
  const extensions = [
    StarterKit.configure({
      heading: { levels: [2, 3, 4] },
      ...(prose ? { underline: marks.includes("underline") ? undefined : false, code: marks.includes("code") ? undefined : false, codeBlock: false } : {}),
      ...(hardBreak ? {} : { hardBreak: false }),
      ...(document ? { document: false } : {}),
      link: false,
    }),
    EntityLink.configure({
      openOnClick: false,
      HTMLAttributes: { class: linkClass, target: null, rel: null },
      siteRel,
      siteHosts,
      entityClass: entityLinkClass,
    }),
    ...(document ? [document] : []),
  ];
  if (!prose) {
    extensions.push(
      image.configure({ HTMLAttributes: { class: imageClass } }),
      Placeholder.configure({ placeholder }),
      Table.configure({ resizable: false, HTMLAttributes: { class: "tiptap-table" } }),
      TableRow,
      TableHeader,
      TableCell,
    );
  }
  return extensions;
}

/** Empty documents are stored as "". */
export function normalizeEmpty(html) {
  return html === "<p></p>" || html === "" ? "" : html;
}
