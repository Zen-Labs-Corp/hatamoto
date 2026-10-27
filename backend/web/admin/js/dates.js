// One display format for every date the admin shows client-side: day-month order, 24h clock,
// matching the server (format.go). en-GB writes "Sept"; the server writes "Sep", so it is
// normalised here. No side effects, so Node can import it.
const fmt = (o) => new Intl.DateTimeFormat("en-GB", o);
export const shortMonth = (s) => s.replace(/\bSept\b/, "Sep");
export const hmDate = (d) => shortMonth(fmt({ day: "numeric", month: "short", year: "numeric" }).format(d));
export const hmTime = (d) => fmt({ hour: "2-digit", minute: "2-digit", hour12: false }).format(d);
export const hmDateTime = (d) => `${hmDate(d)} ${hmTime(d)}`;
