// The scanner paths the server treats as probes, read (never edited) from the server source so the
// list cannot drift from the code it checks: the quoted paths of wellKnownGroups and the prefixes
// of wellKnownPrefixes (each prefix gets one child path). Fails loudly when the parse finds too
// few, so a changed file layout never shrinks the check silently.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = path.join(here, "..", "..", "backend", "internal", "server", "notfound_wellknown.go");
export const MIN_PATHS = 200;

export function scannerPaths() {
  const src = fs.readFileSync(source, "utf8");
  const groups = src.match(/var wellKnownGroups = [\s\S]*?\n}\n/);
  const prefixes = src.match(/var wellKnownPrefixes = [\s\S]*?\n}\n/);
  if (!groups || !prefixes) throw new Error("scanner-paths: cannot find wellKnownGroups or wellKnownPrefixes in " + path.basename(source));
  const paths = new Set();
  for (const m of groups[0].matchAll(/"(\/[^"\s]*)"/g)) paths.add(m[1]);
  for (const m of prefixes[0].matchAll(/\{"(\/[^"\s]*)",/g)) paths.add(m[1].endsWith("/") ? m[1] + "index.php" : m[1] + "/index.php");
  const out = [...paths];
  if (out.length < MIN_PATHS) throw new Error(`scanner-paths: parsed only ${out.length} paths (need ${MIN_PATHS}); the parser or the source layout changed`);
  return out;
}
