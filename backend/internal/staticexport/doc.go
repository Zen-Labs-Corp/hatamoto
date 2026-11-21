// Package staticexport turns the public site into plain files: the URL
// inventory and crawl, the content-hash manifest and diff, the _redirects
// file and redirect stubs, and the directory and S3 publishers. Rendering
// itself is the server's: a Source fetches each path through the real router.
package staticexport
