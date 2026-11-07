// What a click on a link in the bar or the tabs does while the form has unsaved changes. Pure, so it
// is unit-tested. s: {dirty, state} of the saver.
// "go": let the browser follow the link (beforeunload still asks when changes are unsaved).
// "save": hold the click, save first, then ask afterLeave.

/** "go" when nothing is unsaved or saving is stopped (a conflict or a lost session); else "save". */
export function leaveMode(s) {
  return !s.dirty || s.state === "conflict" || s.state === "stopped" ? "go" : "save";
}

/** After the save a link click waited for: "go" when it is clean, "ask" when changes remain and the page may not be left silently, "stay" when the save just ended in a stop (the bar says why; the next click goes). */
export function afterLeave(s) {
  if (!s.dirty) return "go";
  return s.state === "conflict" || s.state === "stopped" ? "stay" : "ask";
}
