package admin

import "time"

// Display date formats shared by every admin page: day-month order, 24h clock.
// Callers apply any zone (.In/.UTC) before formatting; machine layouts such as
// dayLayout and scheduleLayout are separate and never shown to users.

func fmtDate(t time.Time) string     { return t.Format("2 Jan 2006") }
func fmtDateTime(t time.Time) string { return t.Format("2 Jan 2006 15:04") }
func fmtTime(t time.Time) string     { return t.Format("15:04") }
