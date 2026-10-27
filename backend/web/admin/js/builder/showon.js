// Show on: three independent switches (Desktop, Tablet, Phone) over the existing hide / display style
// keys (a lazy chunk, pure). Turning one width on or off never changes another: the writes it returns
// are computed for every width, so a narrower width that only looked hidden or shown because a wider one
// was keeps its own visibility. The server's cascade is desktop-first (a hide at Desktop covers narrower
// widths unless they set display), and hiddenAt() in bp.js mirrors it.
import { BPS, hiddenAt, natural } from "@hm/builder/bp";

/** [{bp, shown}] for each width. */
export const showOnState = (node) => BPS.map((bp) => ({ bp, shown: !hiddenAt(node, bp) }));

/**
 * The style writes [{bp, key, value}] that show (on) or hide (!on) node at `bp` and leave the other widths as they are.
 * Showing a width a wider one hides writes the kind's natural display there (kindDisplay, from the kinds table); a display the author set is kept.
 * Showing a width also clears the natural display that hiding it wrote at narrower widths, once nothing needs it.
 */
export function showOnWrites(node, bp, on, kindDisplay) {
  const want = Object.fromEntries(BPS.map((b) => [b, b === bp ? !on : hiddenAt(node, b)])); // true = hidden
  const sim = Object.fromEntries(Object.entries(node.s ?? {}).map(([k, m]) => [k, { ...m }]));
  const now = () => ({ ...node, s: sim });
  const out = [];
  const put = (b, key, value) => {
    out.push({ bp: b, key, value });
    const m = (sim[b] ??= {});
    if (value === null) delete m[key];
    else m[key] = value;
  };
  for (const b of BPS) {
    if (hiddenAt(now(), b) === want[b]) continue;
    if (want[b]) {
      put(b, "hide", "1");
      continue;
    }
    if (sim[b]?.hide) put(b, "hide", null);
    if (hiddenAt(now(), b)) put(b, "display", natural(now(), b, kindDisplay));
  }
  // Showing a width: a narrower width's display that equals the natural one was most likely written by hiding this width
  // (see above). Left behind it would keep overriding the layout (a container turned from flex to grid stays flex), so
  // it goes when every width keeps its visibility without it.
  if (on) {
    for (const b of BPS.slice(BPS.indexOf(bp) + 1)) {
      const d = sim[b]?.display;
      if (!d || d === "none" || d !== natural(now(), b, kindDisplay)) continue;
      put(b, "display", null);
      if (BPS.some((x) => hiddenAt(now(), x) !== want[x])) {
        out.pop();
        sim[b].display = d;
      }
    }
  }
  return out;
}
