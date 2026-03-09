package server

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

//go:embed static
var staticFS embed.FS

// spaHandler serves the embedded frontend static files.
// It falls back to index.html for client-side routing paths.
func spaHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// If static dir doesn't exist (dev mode), return a handler that says so.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not built – run 'make web'", http.StatusNotFound)
		})
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't serve SPA for API routes.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the static file directly.
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if file exists in the embedded FS.
		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for SPA client-side routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// devFrontendDir returns the path to the web dev server if PCAPCHU_DEV is set.
func devFrontendDir() string {
	return os.Getenv("PCAPCHU_FRONTEND_DIR")
}
