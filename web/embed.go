// Package web embeds the built Svelte frontend (web/dist) into the Go binary so
// Setu ships as a single static executable with no external asset files.
//
// The embed lives here, beside the frontend source, because //go:embed paths
// cannot reach across directories with "..". The Go API layer imports this
// package and serves Dist() with an SPA fallback.
//
// web/dist is produced by `make web` (or the Docker build) and is git-ignored;
// only a .gitkeep is committed so the directory exists and `//go:embed` compiles
// on a fresh checkout. Until the frontend is built the embed contains no
// index.html, and the API serves a small built-in placeholder page instead (see
// internal/api/static.go). The canonical run paths build the frontend first.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distEmbed embed.FS

// Dist returns the embedded frontend build, rooted at the dist directory.
func Dist() fs.FS {
	sub, err := fs.Sub(distEmbed, "dist")
	if err != nil {
		// dist is always embedded at build time; a failure here is a bug.
		panic(err)
	}
	return sub
}
