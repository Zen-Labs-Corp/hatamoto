// Selection facts shared by the Navigator, the canvas toolbar and the Inspector header. Pure: no DOM, no screen import.
// Two id spaces exist: a collapsed theme section is one Navigator row keyed by its container id, while the canvas keys
// it by its component id. Every function accepts either and normalises with rowIdOf. locate() is O(n): call these once per draw.
import { locate, moveSubject } from "@hm/builder/store";

/** Row-collapse predicate (presentation only). Looser than store.v1Shaped: styles are allowed. The Navigator applies it to top-level rows only: a nested container(tag=section) stays two rows. */
export const isCollapsed = (n) => !!n && n.t === "container" && n.p?.tag === "section" && (n.c ?? []).length === 1 && n.c[0].t === "component";

/** Navigator row id of any node: a component under a collapsed section maps to its container; else id itself. null when unknown. */
export function rowIdOf(doc, id) {
  const l = locate(doc, id);
  return !l ? null : l.node.t === "component" && l.depth === 2 && isCollapsed(l.parent) ? l.parentId : id;
}

/** The top-level section row containing id (or id itself): {id, node, index, collapsed} or null. */
export function sectionOf(doc, id) {
  const l = locate(doc, id);
  if (!l) return null;
  const node = doc.nodes[l.path[0]];
  return { id: node.id, node, index: l.path[0], collapsed: isCollapsed(node) };
}

/** What Esc / "up to parent" selects: null at section level (the caller may deselect). A block of a collapsed section's component goes to the container. */
export function parentOf(doc, id) {
  const l = locate(doc, id);
  if (!l || l.depth === 1) return null;
  const up = rowIdOf(doc, l.parentId);
  return up === rowIdOf(doc, id) ? parentOf(doc, up) : up;
}

/** Facts for the toolbar, band and Inspector header. depth is the ROW level: blocks inside a collapsed section count one less than their tree depth. */
export function selectionInfo(doc, id) {
  const l = locate(doc, id);
  if (!l) return null;
  const rowId = rowIdOf(doc, id);
  const parentId = parentOf(doc, id);
  const section = sectionOf(doc, id);
  const inCollapsed = section.collapsed && l.depth > 1;
  const depth = l.depth - (inCollapsed ? 1 : 0) || 1;
  return {
    id,
    rowId,
    kind: l.node.t,
    parentId,
    parentKind: parentId ? locate(doc, parentId).node.t : null,
    section,
    depth,
    isSection: depth === 1,
    childCount: (l.node.c ?? []).length,
    subject: moveSubject(doc, id),
  };
}

/** Next (dir 1) or previous (dir -1) top-level section id from the selection; null at the ends (no wrap) or when unknown. */
export function stepSection(doc, id, dir) {
  const s = sectionOf(doc, id);
  return s ? (doc.nodes[s.index + dir]?.id ?? null) : null;
}

/** True when id lies inside section row sectionId without being that row itself (drives the tinted band). */
export const inSection = (doc, sectionId, id) => sectionOf(doc, id)?.id === sectionId && rowIdOf(doc, id) !== sectionId;
