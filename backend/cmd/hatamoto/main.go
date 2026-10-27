// Command hatamoto is the single hatamoto binary:
//
//	hatamoto serve                  migrate, then run the site, API, admin and worker
//	hatamoto migrate [status]       database migrations
//	hatamoto user create|reset-password
//	hatamoto entity|redirect|analytics ... | export | import   (see docs/CLI.md)
//	hatamoto admin-url [set PATH [--i-understand-weak] | random]
//	hatamoto routes | version | plugins list
//
// Everything lives in package app.
package main

import "hatamoto.dev/core/app"

func main() { app.Main() }
