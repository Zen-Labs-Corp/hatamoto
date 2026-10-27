// Date range calendar for hm/daterange, ported from the SimpleOnboarding
// DateRangePicker (itself after Tremor): a month grid with round ink end
// points and a soft band between them. The first click picks the start, the
// second the end and applies, submitting the picker's GET form. The From/To
// inputs stay in the form and carry the values; without this script they are
// the picker. Dates are UTC days ("2026-10-19"), like the server's range.
const DAY = 864e5;
const monthName = new Intl.DateTimeFormat("en-GB", { month: "long", year: "numeric", timeZone: "UTC" });
const longDate = new Intl.DateTimeFormat("en-GB", { weekday: "long", month: "long", day: "numeric", year: "numeric", timeZone: "UTC" });
const WEEKDAYS = [["Sun", "Sunday"], ["Mon", "Monday"], ["Tue", "Tuesday"], ["Wed", "Wednesday"], ["Thu", "Thursday"], ["Fri", "Friday"], ["Sat", "Saturday"]];

const iso = (d) => d.toISOString().slice(0, 10);
const parse = (s) => (/^\d{4}-\d{2}-\d{2}$/.test(s || "") ? new Date(`${s}T00:00:00Z`) : null);
const addDays = (d, n) => new Date(d.getTime() + n * DAY);
const monthOf = (d) => new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1));
const addMonths = (d, n) => new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + n, 1));
const chevron = (d) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="${d}"/></svg>`;

function enhance(pop) {
  if (pop.dataset.hmCal) return;
  const form = pop.querySelector("form");
  const from = form?.querySelector('input[name="from"]');
  const to = form?.querySelector('input[name="to"]');
  const fields = pop.querySelector(".hm-daterange__dates");
  if (!from || !to || !fields) return;
  pop.dataset.hmCal = "1";

  // The compare switch applies at once, like a preset.
  pop.addEventListener("change", (evt) => {
    if (evt.target.matches('input[role="switch"]')) evt.target.form.requestSubmit();
  });

  const min = parse(from.min);
  const max = parse(from.max);
  const s = { start: null, end: null, pending: null, hover: null, focus: null, view: null };
  const reset = () => {
    s.start = parse(from.value);
    s.end = parse(to.value);
    s.pending = s.hover = null;
    s.focus = s.end || s.start || max || parse(iso(new Date()));
    s.view = monthOf(s.focus);
  };
  reset();

  const cal = document.createElement("div");
  cal.className = "hm-cal";
  cal.innerHTML =
    `<div class="hm-cal__head"><button type="button" class="hm-cal__nav" data-step="-1" aria-label="Previous month">${chevron("m15 18-6-6 6-6")}</button>` +
    `<p class="hm-cal__month" aria-live="polite"></p>` +
    `<button type="button" class="hm-cal__nav" data-step="1" aria-label="Next month">${chevron("m9 18 6-6-6-6")}</button></div>` +
    `<table class="hm-cal__grid"><thead><tr>${WEEKDAYS.map(([a, l]) => `<th scope="col" abbr="${l}">${a}</th>`).join("")}</tr></thead><tbody></tbody></table>` +
    `<p class="hm-cal__hint" aria-live="polite"></p>`;
  fields.hidden = true;
  fields.after(cal);
  const body = cal.querySelector("tbody");
  const title = cal.querySelector(".hm-cal__month");
  const hint = cal.querySelector(".hm-cal__hint");
  const [prev, next] = cal.querySelectorAll(".hm-cal__nav");

  const allowed = (d) => (!min || d >= min) && (!max || d <= max);

  // render builds the six weeks of the view month; paint only moves the
  // classes, so a hover never rebuilds the button under the pointer.
  function render() {
    title.textContent = monthName.format(s.view);
    prev.disabled = Boolean(min && addDays(s.view, -1) < min);
    next.disabled = Boolean(max && addMonths(s.view, 1) > max);
    let d = addDays(s.view, -s.view.getUTCDay());
    let rows = "";
    for (let w = 0; w < 6; w++) {
      rows += "<tr>";
      for (let i = 0; i < 7; i++, d = addDays(d, 1)) {
        const out = d.getUTCMonth() !== s.view.getUTCMonth();
        rows += `<td class="hm-cal__cell"><button type="button" class="hm-cal__day${out ? " hm-cal__day--out" : ""}" data-day="${iso(d)}" aria-label="${longDate.format(d)}" tabindex="-1"${allowed(d) ? "" : " disabled"}>${d.getUTCDate()}</button></td>`;
      }
      rows += "</tr>";
    }
    body.innerHTML = rows;
    paint();
  }

  function paint() {
    const lo = s.pending || s.start;
    const hi = s.pending ? (s.hover && s.hover >= s.pending ? s.hover : null) : s.end;
    const loIso = lo && iso(lo);
    const hiIso = hi && iso(hi);
    const focusIso = s.focus && iso(s.focus);
    let focusable = null;
    for (const btn of body.querySelectorAll(".hm-cal__day")) {
      const day = btn.dataset.day;
      const cell = btn.parentElement;
      const inView = !btn.classList.contains("hm-cal__day--out");
      const end = day === loIso || (!s.pending && day === hiIso);
      btn.classList.toggle("hm-cal__day--end", end);
      btn.setAttribute("aria-pressed", String(end));
      cell.classList.toggle("hm-cal__cell--in", Boolean(inView && loIso && hiIso && day >= loIso && day <= hiIso));
      cell.classList.toggle("hm-cal__cell--start", Boolean(hiIso) && day === loIso);
      cell.classList.toggle("hm-cal__cell--end", Boolean(hiIso) && day === hiIso);
      if (day === focusIso && !btn.disabled) focusable = btn;
    }
    // One tab stop in the grid (roving tabindex): the focused day, else the first enabled one.
    (focusable || body.querySelector(".hm-cal__day:not([disabled])"))?.setAttribute("tabindex", "0");
    hint.textContent = s.pending ? `Start ${longDate.format(s.pending)}. Pick the end date.` : "Pick a start date, then an end date.";
  }

  function pick(d) {
    if (!allowed(d)) return;
    s.focus = d;
    if (!s.pending || d < s.pending) {
      s.pending = d;
      s.hover = null;
      paint();
      return;
    }
    from.value = iso(s.pending);
    to.value = iso(d);
    form.requestSubmit();
  }

  function moveFocus(d) {
    if ((min && d < min) || (max && d > max)) return;
    s.focus = d;
    if (s.pending) s.hover = d;
    if (monthOf(d).getTime() !== s.view.getTime()) {
      s.view = monthOf(d);
      render();
    } else {
      for (const b of body.querySelectorAll('.hm-cal__day[tabindex="0"]')) b.setAttribute("tabindex", "-1");
      paint();
    }
    body.querySelector(`[data-day="${iso(d)}"]`)?.focus();
  }

  cal.addEventListener("click", (evt) => {
    const nav = evt.target.closest(".hm-cal__nav");
    if (nav) {
      s.view = addMonths(s.view, Number(nav.dataset.step));
      render();
      return;
    }
    const btn = evt.target.closest(".hm-cal__day");
    if (btn && !btn.disabled) pick(parse(btn.dataset.day));
  });
  body.addEventListener("mouseover", (evt) => {
    const btn = evt.target.closest(".hm-cal__day");
    if (!s.pending || !btn || btn.disabled || btn.dataset.day === (s.hover && iso(s.hover))) return;
    s.hover = parse(btn.dataset.day);
    paint();
  });
  body.addEventListener("keydown", (evt) => {
    const btn = evt.target.closest(".hm-cal__day");
    if (!btn) return;
    const d = parse(btn.dataset.day);
    const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[evt.key];
    if (step) moveFocus(addDays(d, step));
    else if (evt.key === "PageUp" || evt.key === "PageDown") moveFocus(new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + (evt.key === "PageUp" ? -1 : 1), Math.min(d.getUTCDate(), 28))));
    else if (evt.key === "Home") moveFocus(addDays(d, -d.getUTCDay()));
    else if (evt.key === "End") moveFocus(addDays(d, 6 - d.getUTCDay()));
    else return;
    evt.preventDefault();
  });
  // Closing the popover drops a half-picked range.
  pop.addEventListener("toggle", (evt) => {
    if (evt.newState === "closed") {
      reset();
      render();
    }
  });
  render();
}

function init(root) {
  for (const pop of root.querySelectorAll("[data-hm-daterange]")) enhance(pop);
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => init(document));
else init(document);
document.addEventListener("htmx:load", (evt) => init(evt.target));
export { init };
