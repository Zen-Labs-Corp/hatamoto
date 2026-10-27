// Number and date formatting for the AI analytics charts, ported from the
// approved prototype (helpers and axis formats). No DOM, no clock, no side
// effects, so Node can import it. Dates are ISO "YYYY-MM-DD" strings read in
// UTC, the way the server buckets them.

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const DAY_MS = 864e5;

const parse = (iso) => {
  const [y, m, d] = String(iso).slice(0, 10).split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d));
};
const toISO = (date) => date.toISOString().slice(0, 10);

/** 55574 -> "55,574" (en-GB grouping, rounded). */
export const num = (n) => Math.round(n).toLocaleString("en-GB");

// One decimal under 10, whole numbers from 10 up.
const trim1 = (v) => (v >= 10 ? String(Math.round(v)) : String(Math.round(v * 10) / 10));

/** Axis style: 1200 -> "1.2K", 2500000 -> "2.5M", under 1000 as it is. */
export const compact = (n) => (n >= 1e6 ? trim1(n / 1e6) + "M" : n >= 1e3 ? trim1(n / 1e3) + "K" : String(Math.round(n * 10) / 10));

/** 3.84 -> "3.8%". The prototype default is one decimal. */
export const percent = (v, digits = 1) => v.toFixed(digits) + "%";

/** Share style (bar lists): one decimal under 10, whole numbers above. */
export const sharePercent = (v) => percent(v, Math.round(v * 10) / 10 < 10 ? 1 : 0);

/** "2026-10-23" -> "23 Oct". */
export function dayLabel(iso) {
  const d = parse(iso);
  return d.getUTCDate() + " " + MONTHS[d.getUTCMonth()];
}

/** "2026-10-23" -> "23 Oct 2026". */
export function longDate(iso) {
  return dayLabel(iso) + " " + parse(iso).getUTCFullYear();
}

/** The Monday (UTC) of the ISO week that contains the date. */
export function isoMonday(iso) {
  const d = parse(iso);
  const back = (d.getUTCDay() + 6) % 7;
  return toISO(new Date(d.getTime() - back * DAY_MS));
}

/** "Week of 20 Oct": weeks are named by their Monday. */
export const weekLabel = (iso) => "Week of " + dayLabel(isoMonday(iso));
