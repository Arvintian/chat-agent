// Package web provides embedded static files and HTTP handlers for the chat-agent web UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFiles embed.FS

// GetFS returns the embedded static files as a http.FileSystem.
func GetFS() http.FileSystem {
	files, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(files)
}

// StaticHandler returns an HTTP handler that serves embedded static files
// with a Cache-Control header allowing browser caching for up to 1 day.
func StaticHandler() http.Handler {
	fs := http.FileServer(GetFS())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fs.ServeHTTP(w, r)
	})
}

// IndexHandler returns an HTTP handler that serves the main index.html for the root path.
func IndexHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, "static/index.html")
		}
	})
}