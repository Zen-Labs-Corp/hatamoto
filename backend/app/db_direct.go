package app

import (
	"os"
	"strings"
)

// directDatabaseURL is the database URL for tools that need a real server
// session (pg_dump, pg_restore): DATABASE_DIRECT_URL when set, otherwise
// DATABASE_URL. It differs from DATABASE_URL only behind a connection pooler.
func directDatabaseURL() string {
	if v := strings.TrimSpace(os.Getenv("DATABASE_DIRECT_URL")); v != "" {
		return v
	}
	return os.Getenv("DATABASE_URL")
}
