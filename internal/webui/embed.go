// Package webui embeds the Vite-built operator dashboard and serves it as an SPA.
//
// Build the UI first (make build-ui or make build) so dist/ exists:
//
//	cd web && npm ci && npm run build
//
// Output lands in internal/webui/dist and is embedded into temperci-control.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Dist holds the Vite production build (index.html + assets/).
//
//go:embed all:dist
var Dist embed.FS

// SPAHandler serves the embedded Vite app with client-side routing fallback.
func SPAHandler() http.Handler {
	sub, err := fs.Sub(Dist, "dist")
	if err != nil {
		// Should not happen when dist is embedded correctly.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "dashboard assets missing; run: make build-ui", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never take over API/agent/webhook/metrics routes (registered more specifically on the mux).
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasPrefix(r.URL.Path, "/v1/") ||
			strings.HasPrefix(r.URL.Path, "/webhooks/") ||
			strings.HasPrefix(r.URL.Path, "/webhook/") ||
			r.URL.Path == "/healthz" ||
			r.URL.Path == "/metrics" {
			http.NotFound(w, r)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Serve real files (hashed assets, index.html, favicon, etc.).
		if f, err := sub.Open(path); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: client routes like /hosts, /jobs
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r2)
	})
}
