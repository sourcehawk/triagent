// Package web exposes the embedded Next.js static bundle used by the
// internal/server HTTP handler.
//
// Layout: `frontend/` (the Next.js project at the plugin root) builds into
// `frontend/out/`; `make build` copies that tree to `internal/web/dist/`,
// which lives next to this file so Go's embed pattern can reach it.
//
// A tracked `dist/.gitkeep` gives //go:embed at least one match on a fresh
// checkout — before `make build` populates the directory the embedded FS
// is effectively empty, and the static handler will 404 until a real
// bundle is in place. This is intentional: it keeps the embed artefact
// out of git without losing compile-on-fresh-checkout.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the static frontend filesystem rooted at the dist/ subtree.
// Callers wrap it with http.FS for serving via http.FileServer.
func FS() (fs.FS, error) { return fs.Sub(distFS, "dist") }
