// Package web embeds the site's on-disk assets (static files, mockups, and
// later the templates) so the server binary is self-contained. Server code
// takes fs.Sub of the subdirectories it needs; in -dev mode cmd/server
// substitutes os.DirFS("web") so edits show up without a rebuild.
package web

import "embed"

// Files is everything under web/. The wildcard (rather than naming static,
// mockups, templates explicitly) keeps the build green even while a
// subdirectory (e.g. mockups/) is empty or absent — go:embed errors on a
// named pattern that matches nothing, while requests for files missing from
// a wildcard embed simply 404.
//
//go:embed all:*
var Files embed.FS
