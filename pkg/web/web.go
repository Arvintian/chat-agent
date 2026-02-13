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

// IndexHandler returns an HTTP handler that serves the main index.html for the root path.
func IndexHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, "static/index.html")
		}
	})
}