// Package web embeds the committed frontend build.
//
// The frontend is built once (npm ci && vite build) and committed under
// internal/web/dist/. This keeps the Go backend buildable without Node.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// HasHTML reports whether the embedded build contains an index.html.
var HasHTML = func() bool {
	_, err := distFS.ReadFile("dist/index.html")
	return err == nil
}()

// Index returns the embedded index.html bytes.
func Index() []byte {
	b, _ := distFS.ReadFile("dist/index.html")
	return b
}

// Handler serves the embedded static assets.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}

// DistFS exposes the embedded filesystem for tests.
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	return sub
}
