// Pure derivation of form fields from an html-kind value, plus the element-addressed
// writers (port of canvas/html-fields.ts). Text fields address text nodes exactly like
// htmltext.js (one walk, never forked); attribute and prose-region writes address
// elements by a childNodes index path from the fragment root and carry an `expect`
// guard so a stale UI can never overwrite newer markup. Needs a DOM; no other I/O.
//
// A field is one of {kind: text|attr|links|prose|elementText|group, key, label, ...}
// (see deriveHtmlFields); writers return {ok:true, html} or {ok:false, reason}.
import { BLANK, SKIP, collect, fragment, replaceTextNode } from "@hm/editor/htmltext";
const PROSE_BLOCKS = new Set(['p', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'blockquote', 'hr']);
const PROSE_DESCENDANTS = new Set([...Array.from(PROSE_BLOCKS), 'li', 'a', 'strong', 'em', 'b', 'i', 's', 'br']);
const MIN_LINK_RUN = 3;
const tagOf = (el) => el.localName.toLowerCase();
/** `<prefix>-<name>` tokens (`x-lede`, `site-lede`) are labelled by the name; a single word stays as is. */
function humanise(token) {
  const segments = token.split(/[-_]+/).filter(Boolean);
  const words = (segments.length > 1 ? segments.slice(1) : segments).join(' ');
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : '';
}
const classTokens = (el) => (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean);
/** Humanised class token (`x-<name>` wins, else the last token); '' unless >= 3 letters and not just the tag name. */
function classLabel(el) {
  const tokens = classTokens(el);
  const token = tokens.find((t) => /^x-.+/.test(t)) ?? tokens[tokens.length - 1];
  const label = token ? humanise(token) : '';
  return label.replace(/[^a-z]/gi, '').length >= 3 && label.toLowerCase() !== tagOf(el) ? label : '';
}
const TAG_LABELS = {
  span: 'Text',
  div: 'Text',
  li: 'List item',
  td: 'Cell',
  th: 'Column heading',
  summary: 'Question',
  dt: 'Term',
  dd: 'Description',
  figcaption: 'Caption',
  blockquote: 'Quote',
  cite: 'Source',
  button: 'Button',
  label: 'Label',
  a: 'Link text',
};
function elementLabel(el, inItem) {
  const tag = tagOf(el);
  const fromClass = classLabel(el);
  if (fromClass && classTokens(el).some((t) => /^x-.+/.test(t)))
    return fromClass;
  if (tag === 'h1')
    return 'Heading';
  if (/^h[2-6]$/.test(tag))
    return inItem && tag !== 'h2' ? 'Title' : 'Subheading';
  if (tag === 'p')
    return 'Text';
  return fromClass || TAG_LABELS[tag] || humanise(tag);
}
const INLINE = new Set(['strong', 'em', 'b', 'i', 's', 'u', 'span', 'small', 'mark', 'sub', 'sup', 'code']);
/** Label for a text node in `parent`: inline wrappers defer to the nearest block ancestor. */
function textLabel(parent, inItem) {
  let el = parent;
  while (INLINE.has(tagOf(el)) && !classTokens(el).length && el.parentElement)
    el = el.parentElement;
  return elementLabel(el, inItem);
}
function runLabel(parent) {
  return parent.getAttribute('aria-label')?.trim() || classLabel(parent) || 'Links';
}
function isProseRegion(el, minBlocks = 2) {
  let blocks = 0;
  for (const c of Array.from(el.childNodes)) {
    if (c.nodeType === 3) {
      if (!BLANK.test(c.data))
        return false;
    }
    else if (c.nodeType === 1) {
      if (!PROSE_BLOCKS.has(tagOf(c)))
        return false;
      blocks++;
    }
  }
  if (blocks < minBlocks)
    return false;
  return Array.from(el.querySelectorAll('*')).every((d) => PROSE_DESCENDANTS.has(tagOf(d)) && !SKIP.has(tagOf(d)));
}
function isBlankText(n) {
  return n.nodeType === 3 && BLANK.test(n.data);
}
const INLINE_RICH = new Set(['a', 'strong', 'em', 'b', 'i', 's', 'br']);
/** Elements that keep a field when emptied, so a cleared region can be refilled. */
const EMPTY_LEAVES = new Set(['p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'li', 'span', 'summary', 'dt', 'dd', 'figcaption']);
const INLINE_BLOCKS = new Set(['p', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'dd', 'td', 'blockquote']);
const LEAF_ITEMS = new Set(['li', 'span', 'dt', 'dd', 'td', 'th']);
const ITEM_TITLE = 'h1,h2,h3,h4,h5,h6,summary,dt';
const MAX_TITLE = 48;
/** Siblings of these tags never group on tag + classes alone (headings stay with their paragraphs). */
const NO_LOOSE_GROUP = new Set(['p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'img']);
const MIN_LOOSE_RUN = 3;
const elementKids = (el) => Array.from(el.children).filter((c) => !SKIP.has(tagOf(c)));
const hasText = (el) => Array.from(el.childNodes).some((c) => c.nodeType === 3 && !BLANK.test(c.data));
/** No element children and no visible text. */
const isEmptyLeaf = (el) => EMPTY_LEAVES.has(tagOf(el)) && el.children.length === 0 && Array.from(el.childNodes).every((c) => c.nodeType !== 3 || BLANK.test(c.data));
/** Text mixed with at least one inline-rich element, and nothing but inline-rich elements below. */
function isInlineRich(el) {
  if (!INLINE_BLOCKS.has(tagOf(el)) || !hasText(el))
    return false;
  const all = Array.from(el.querySelectorAll('*'));
  return all.length > 0 && all.every((d) => INLINE_RICH.has(tagOf(d)));
}
/**
* Shape signature: tag + sorted classes + child shapes. Text is ignored, and an
* inline-rich block collapses to a leaf so `<p>x</p>` and `<p>x <a>y</a></p>` match.
*/
function shape(el) {
  const kids = isInlineRich(el) ? [] : Array.from(el.children).map(shape);
  return `${tagOf(el)}.${classTokens(el).sort().join('.')}(${kids.join(',')})`;
}
function itemTitle(el, n) {
  const clean = (t) => t.replace(/\s+/g, ' ').trim();
  const head = el.matches(ITEM_TITLE) ? el : tagOf(el) === 'details' ? el.querySelector(':scope > summary') : el.querySelector(ITEM_TITLE);
  const text = clean(head?.textContent ?? '') || clean(collect(el)[0]?.data ?? '');
  if (!text)
    return `Item ${n}`;
  return text.length > MAX_TITLE ? `${text.slice(0, MAX_TITLE - 1)}…` : text;
}
/** Siblings sharing a label inside one item get numbered: 'Text 1', 'Text 2'. */
function numberLabels(fields) {
  const counts = new Map();
  const labelled = (f) => f.kind === 'text' || f.kind === 'prose' || f.kind === 'elementText';
  for (const f of fields)
    if (labelled(f))
      counts.set(f.label, (counts.get(f.label) ?? 0) + 1);
  const seen = new Map();
  return fields.map((f) => {
    if (!labelled(f) || (counts.get(f.label) ?? 0) < 2)
      return f;
    const n = (seen.get(f.label) ?? 0) + 1;
    seen.set(f.label, n);
    return { ...f, label: `${f.label} ${n}` };
  });
}
/**
* The `<p>` the rich editor is asked to round-trip for an inline region. The editor
* writes bold/italic as strong/em, so b/i are probed as those (same rendering).
*/
function inlineProbe(el) {
  const p = fragment(`<p>${el.innerHTML}</p>`);
  for (const n of Array.from(p.querySelectorAll('b,i'))) {
    const twin = n.ownerDocument.createElement(tagOf(n) === 'b' ? 'strong' : 'em');
    while (n.firstChild)
      twin.appendChild(n.firstChild);
    for (const a of Array.from(n.attributes))
      twin.setAttribute(a.name, a.value);
    n.replaceWith(twin);
  }
  return p.innerHTML;
}
/** childNodes index path from `root` to `node`. */
function pathOf(root, node) {
  const path = [];
  for (let n = node; n !== root && n.parentNode; n = n.parentNode) {
    path.unshift(Array.prototype.indexOf.call(n.parentNode.childNodes, n));
  }
  return path;
}
/** Derive the editable fields of `html`. Never mutates anything. */
export function deriveHtmlFields(html, opts) {
  const root = fragment(html);
  const textIndex = new Map();
  collect(root).forEach((n, i) => textIndex.set(n, i));
  const attrField = (el, path, attr, label, group) => ({
    kind: 'attr',
    key: `a${path.join('.')}:${attr}`,
    label,
    path,
    attr,
    value: el.getAttribute(attr) ?? '',
    group,
  });
  const textField = (node, label, group) => {
    const i = textIndex.get(node);
    // The key is the node's address, not its text index: indexes shift when another region gains text.
    const path = pathOf(root, node);
    return i === undefined ? [] : [{ kind: 'text', key: `t${path.join('.')}`, label, textIndex: i, path, value: node.data, group }];
  };
  const textFields = (el, label, group) => Array.from(el.childNodes).flatMap((c) => textField(c, label, group));
  /** A link with element children (card link) offers its URL, then everything inside it. */
  const linkFields = (a, path, group, inItem = false) => a.children.length
    ? [attrField(a, path, 'href', 'URL', group), ...walkChildren(a, path, group, inItem)]
    : [...textFields(a, 'Link text', group), attrField(a, path, 'href', 'URL', group)];
  const imageFields = (img, path, group) => [
    attrField(img, path, 'src', 'Image URL', group),
    attrField(img, path, 'alt', 'Alt text', group),
  ];
  const isRegion = (el) => isProseRegion(el) && opts.canRoundTrip(el.innerHTML);
  const walk = (el, path, group, inItem) => {
    if (SKIP.has(tagOf(el)))
      return [];
    // Reached directly (a repeated card item) rather than through walkChildren: still offer URL / image fields.
    if (tagOf(el) === 'a')
      return linkFields(el, path, group, inItem);
    if (tagOf(el) === 'img')
      return imageFields(el, path, group);
    if (isEmptyLeaf(el) && (classTokens(el).length || inItem)) {
      return [{ kind: 'elementText', key: `e${path.join('.')}`, label: elementLabel(el, inItem), path, innerHtml: el.innerHTML, value: '', group }];
    }
    if (isRegion(el)) {
      return [{ kind: 'prose', key: `p${path.join('.')}`, label: classLabel(el) || 'Body', path, innerHtml: el.innerHTML, group }];
    }
    // An inline region is edited as one `<p>`; the editor's verdict on that shape decides.
    if (isInlineRich(el) && opts.canRoundTrip(inlineProbe(el))) {
      return [
        { kind: 'prose', key: `p${path.join('.')}`, label: elementLabel(el, inItem), path, innerHtml: el.innerHTML, inline: true, group },
      ];
    }
    return walkChildren(el, path, group, inItem);
  };
  /** Same-shape sibling runs (>= 2) of `parent`, keyed by the index of their first member. */
  const repeatedRuns = (parent) => {
    const bySig = new Map();
    Array.from(parent.childNodes).forEach((c, i) => {
      if (c.nodeType !== 1 || SKIP.has(tagOf(c)))
        return;
      const sig = shape(c);
      // Leaf siblings (p, h3, a, img, ...) stay flat fields; only list-like leaves group.
      if (sig.endsWith('()') && !LEAF_ITEMS.has(tagOf(c)))
        return;
      bySig.set(sig, [...(bySig.get(sig) ?? []), i]);
    });
    const runs = new Map();
    for (const idx of Array.from(bySig.values()))
      if (idx.length >= 2)
        runs.set(idx[0], idx);
    addLooseRuns(parent, runs);
    return runs;
  };
  /**
  * Siblings sharing tag + sorted classes group even when their inner shapes differ: a
  * consecutive run of >= 3, or all of the container's element children (>= 2).
  * Elements already in a shape run are left alone.
  */
  const addLooseRuns = (parent, runs) => {
    const taken = new Set(Array.from(runs.values()).flat());
    const key = (el) => `${tagOf(el)}.${classTokens(el).sort().join('.')}`;
    const kids = Array.from(parent.childNodes);
    const elements = kids.flatMap((c, i) => (c.nodeType === 1 && !SKIP.has(tagOf(c)) ? [i] : []));
    const eligible = (i) => !taken.has(i) && !NO_LOOSE_GROUP.has(tagOf(kids[i]));
    const sameKey = (a, b) => key(kids[a]) === key(kids[b]);
    const allSame = elements.length >= 2 && elements.every((i) => eligible(i) && sameKey(i, elements[0]));
    if (allSame) {
      runs.set(elements[0], elements);
      return;
    }
    let run = [];
    const flush = () => {
      if (run.length >= MIN_LOOSE_RUN)
        runs.set(run[0], run);
      run = [];
    };
    for (let n = 0; n < elements.length; n++) {
      const i = elements[n];
      const prev = n > 0 ? elements[n - 1] : -1;
      const gapText = prev >= 0 && kids.slice(prev + 1, i).some((c) => c.nodeType === 3 && !BLANK.test(c.data));
      if (!eligible(i) || (run.length && (gapText || !sameKey(i, run[0]))))
        flush();
      if (eligible(i))
        run.push(i);
    }
    flush();
  };
  const groupField = (parent, path, idx, group) => {
    const items = idx.map((j, n) => {
      const el = parent.childNodes[j];
      const itemPath = [...path, j];
      return {
        key: `i${itemPath.join('.')}`,
        title: itemTitle(el, n + 1),
        fields: numberLabels(walk(el, itemPath, undefined, true)),
      };
    });
    const flat = items.every((it) => it.fields.length === 1 && ['text', 'prose', 'attr', 'elementText'].includes(it.fields[0].kind));
    if (flat)
      items.forEach((it, n) => (it.fields = [{ ...it.fields[0], label: `${it.fields[0].label} ${n + 1}` }]));
    const fallback = /^(ul|ol)$/.test(tagOf(parent)) ? 'List' : 'Items';
    return {
      kind: 'group',
      key: `g${[...path, idx[0]].join('.')}`,
      label: parent.getAttribute('aria-label')?.trim() || classLabel(parent) || fallback,
      count: items.length,
      items,
      ...(flat ? { flat } : {}),
      group,
    };
  };
  const walkChildren = (parent, path, group, inItem) => {
    const out = [];
    const kids = Array.from(parent.childNodes);
    const runs = repeatedRuns(parent);
    const inRun = new Set(Array.from(runs.values()).flat());
    for (let i = 0; i < kids.length; i++) {
      const c = kids[i];
      if (c.nodeType === 3) {
        out.push(...textField(c, textLabel(parent, inItem), group));
        continue;
      }
      if (c.nodeType !== 1)
        continue;
      const el = c;
      const g = group ?? (path.length === 0 ? elementLabel(el, inItem) : undefined);
      if (inRun.has(i)) {
        const run = runs.get(i);
        if (run)
          out.push(groupField(parent, path, run, path.length === 0 ? undefined : group));
        continue;
      }
      if (tagOf(el) === 'a') {
        // Greedy run of sibling anchors, blank text allowed between.
        let end = i;
        const anchors = [i];
        for (let j = i + 1; j < kids.length; j++) {
          if (isBlankText(kids[j]))
            continue;
          if (kids[j].nodeType === 1 && tagOf(kids[j]) === 'a' && !inRun.has(j)) {
            anchors.push(j);
            end = j;
          }
          else
            break;
        }
        if (anchors.length >= MIN_LINK_RUN) {
          const items = anchors.flatMap((j) => linkFields(kids[j], [...path, j], group, inItem));
          out.push({
            kind: 'links',
            key: `l${[...path, i].join('.')}`,
            label: runLabel(parent),
            items,
            group,
          });
          i = end;
          continue;
        }
        out.push(...linkFields(el, [...path, i], group, inItem));
        continue;
      }
      if (tagOf(el) === 'img') {
        out.push(...imageFields(el, [...path, i], g));
        continue;
      }
      out.push(...walk(el, [...path, i], g, inItem));
    }
    return out;
  };
  if (isRegion(root)) {
    return [{ kind: 'prose', key: 'p', label: 'Body', path: [], innerHtml: root.innerHTML }];
  }
  return walkChildren(root, [], undefined, false);
}
function nodeAt(root, path) {
  let node = root;
  for (const idx of path) {
    if (!Number.isInteger(idx) || idx < 0 || idx >= node.childNodes.length)
      return null;
    node = node.childNodes[idx];
  }
  return node;
}
function elementAt(root, path) {
  const node = nodeAt(root, path);
  return node && (node.nodeType === 1 || node === root) ? node : null;
}
/** Text node at `path`: `replaceTextNode` on whatever index that node has now. */
export function replaceTextAt(html, path, expect, value) {
  const div = fragment(html);
  const node = nodeAt(div, path);
  const index = node && node.nodeType === 3 ? collect(div).indexOf(node) : -1;
  return index < 0 ? { ok: false, reason: 'range' } : replaceTextNode(html, index, expect, value);
}
/** Give the element at `path` the plain text `value` if its inner html currently equals `expect`. */
export function setElementText(html, path, expect, value) {
  const div = fragment(html);
  const el = elementAt(div, path);
  if (!el)
    return { ok: false, reason: 'range' };
  if (el.innerHTML !== expect)
    return { ok: false, reason: 'stale' };
  if (BLANK.test(value))
    return { ok: false, reason: 'blank' };
  el.textContent = value;
  return { ok: true, html: div.innerHTML };
}
/**
* Re-read the live value of every field of an earlier derivation from `html`, by address.
* The structure (kinds, groups, keys, labels) is kept as is. A field whose address no longer
* resolves keeps its last value and turns read-only.
*/
export function refreshHtmlFields(fields, html) {
  const root = fragment(html);
  const refresh = (f) => {
    switch (f.kind) {
      case 'text': {
        const node = nodeAt(root, f.path);
        return node && node.nodeType === 3 ? { ...f, value: node.data, readOnly: undefined } : { ...f, readOnly: true };
      }
      case 'attr': {
        const el = elementAt(root, f.path);
        return el ? { ...f, value: el.getAttribute(f.attr) ?? '', readOnly: undefined } : { ...f, readOnly: true };
      }
      case 'prose': {
        const el = elementAt(root, f.path);
        return el ? { ...f, innerHtml: el.innerHTML, readOnly: undefined } : { ...f, readOnly: true };
      }
      case 'elementText': {
        const el = elementAt(root, f.path);
        return el ? { ...f, innerHtml: el.innerHTML, value: el.textContent ?? '', readOnly: undefined } : { ...f, readOnly: true };
      }
      case 'links':
        return { ...f, items: f.items.map(refresh) };
      case 'group':
        return { ...f, items: f.items.map((it) => ({ ...it, fields: it.fields.map(refresh) })) };
    }
  };
  return fields.map(refresh);
}
/** Set attribute `attr` of the element at `path` if it currently equals `expect` ('' when absent). */
export function setAttr(html, path, attr, expect, value) {
  const div = fragment(html);
  const el = elementAt(div, path);
  if (!el)
    return { ok: false, reason: 'range' };
  if ((el.getAttribute(attr) ?? '') !== expect)
    return { ok: false, reason: 'stale' };
  el.setAttribute(attr, value);
  return { ok: true, html: div.innerHTML };
}
/** Replace the inner html of the element at `path` if it currently equals `expect`. */
export function replaceElementInner(html, path, expect, value) {
  const div = fragment(html);
  const el = elementAt(div, path);
  if (!el)
    return { ok: false, reason: 'range' };
  if (el.innerHTML !== expect)
    return { ok: false, reason: 'stale' };
  el.innerHTML = value;
  return { ok: true, html: div.innerHTML };
}
/**
* Inner html of an inline region from the editor's output. Accepts exactly one
* paragraph/heading (what an inline region round-trips to); anything else is null.
*/
export function inlineRegionInner(editorHtml) {
  const root = fragment(editorHtml);
  const only = root.children.length === 1 && root.childNodes.length === 1 ? root.children[0] : null;
  return only && /^(p|h[1-6])$/.test(tagOf(only)) ? only.innerHTML : null;
}
