// Package route lets a wrapper module answer HTTP requests under its own
// public prefix, for example a webhook receiver.
//
//	func init() {
//		route.Register(route.Mount{
//			Module:  "example",
//			Prefix:  "/example/",
//			Handler: http.HandlerFunc(hook),
//		})
//	}
//
// Core puts no session or API-key check in front of a mounted handler: the
// module authenticates its own callers. Core does apply its sign-in rate
// limiter per client address (5 requests a second, burst 10; the 11th rapid
// request gets 429) and caps the request body (MaxBody, default 1 MiB; a read
// past the cap fails with *http.MaxBytesError). Methods are GET, HEAD, POST,
// PUT, PATCH and DELETE.
//
// A prefix is lowercase, starts and ends with "/" and must not overlap another
// module's. Reserved first segments (the admin, the API, plugin, media, form,
// health and similar core paths) are refused at init. Under /auth/ only the
// paths core serves itself, /auth/login/ and /auth/register/, are refused;
// /auth/sso/ is fine. A prefix under the admin address, under or over
// PUBLIC_API_PREFIX, or on a scanner probe path (such as /wp-admin/) makes
// `hatamoto serve` exit at boot.
//
// A mounted prefix is served before the site access gate, locale handling and
// redirect rules, like the admin: the handler decides who may see what. A
// module whose migrations failed is not mounted (404). Register nothing and
// nothing changes.
package route
