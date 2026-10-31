// The Style tab's one context line: a breakpoint pill, a State dropdown and a short note on what the values set here
// override. Pure text helpers plus the small DOM builder. A lazy chunk (style.js mounts it).
// Run: node --test scripts/admin-qa/unit/builder-inspctx.test.mjs

import { h } from "@hm/builder/dom";
import { BP_NAME, BREAKPOINT_MAX } from "@hm/builder/bp";
import { takesParentHover } from "@hm/builder/store";

/** The State choices: [id, name]; "" is Normal. Parent hover needs a parent that is neither a reference nor a component. */
export const STATE_CHOICES = [["", "Normal"], ["hover", "Hover"], ["focus", "Focus"], ["active", "Pressed"], ["parent-hover", "Parent hover"]];
export const stateChoices = (parent) => STATE_CHOICES.filter(([id]) => id !== "parent-hover" || takesParentHover(parent));

const stateName = (id) => STATE_CHOICES.find(([s]) => s === id)?.[1] ?? id;

/** What the values set at this breakpoint (and state) do, in one short sentence. */
export function overrideNote(device, state = "") {
  const base =
    device === "md" ? `Values set here override Desktop on screens ≤${BREAKPOINT_MAX.md}px.` : device === "sm" ? `Values set here override Tablet on screens ≤${BREAKPOINT_MAX.sm}px.` : "Applies to every screen size unless Tablet or Phone override it.";
  return state ? `${base} Applies only in the ${stateName(state)} state.` : base;
}

let seq = 0;

/** div.hm-bs-ctx: breakpoint pill, State select (aria-label "State"), note. onState(id) runs on change. */
export function contextLine({ device, state = "", choices = STATE_CHOICES, onState }) {
  const note = h("p", { class: "hm-bs-ctx__note", id: `hm-bs-ctx-note-${++seq}`, text: overrideNote(device, state) });
  const select = h("select", { class: "hm-bs-ctx__state", "aria-label": "State", "aria-describedby": note.getAttribute("id") }, ...choices.map(([id, name]) => h("option", { value: id, text: name })));
  select.value = state;
  select.addEventListener("change", () => onState?.(select.value));
  return h("div", { class: "hm-bs-ctx" }, h("span", { class: "hm-bs-ctx__bp", text: BP_NAME[device] ?? BP_NAME.base }), select, note);
}
