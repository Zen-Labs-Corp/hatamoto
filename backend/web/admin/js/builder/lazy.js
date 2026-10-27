// Lazy chunks: code that is not needed for first paint is reached through a literal
// `import()` at the point of use (never modulepreload), so it counts only in the total budget.

/** lazy(load) -> () => Promise; memoised, and a rejection clears the memo. Browsers cache a failed module fetch, so callers tell the user to reload the page. */
export const lazy = (load) => {
  let p = null;
  return () => (p ??= load().catch((e) => { p = null; throw e; }));
};

/** idle(fn): run after first paint without blocking input. */
export const idle = (fn) => ("requestIdleCallback" in window ? window.requestIdleCallback(fn, { timeout: 2000 }) : setTimeout(fn, 200));
