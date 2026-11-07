// The designer's preview validator against the server's fixture. Run: node --test scripts/admin-qa/unit/forms-validate.test.mjs
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const V = await import(new URL("../../../backend/web/admin/js/forms/validate.js", import.meta.url));

const read = (p) => JSON.parse(readFileSync(new URL(p, import.meta.url), "utf8"));
const fixture = read("../../../backend/internal/form/testdata/validation_cases.json");
const freeEmail = read("../../../backend/internal/form/freemail.json").domains;

for (const c of fixture.cases) {
  test(`validate: ${c.name}`, () => {
    const got = V.validate(c.fields, c.data, c.context, freeEmail);
    assert.deepEqual(got.errors, c.want_errors ?? {});
    assert.deepEqual(got.data, c.want_data ?? null);
  });
}

for (const c of fixture.visibility) {
  test(`visible: ${c.name}`, () => {
    assert.deepEqual(V.visible(c.fields, c.data), c.want_visible);
  });
}
