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
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
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
		// Leave API/agent/webhook routes to more specific mux handlers (incl. WebSocket).
		if strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasPrefix(r.URL.Path, "/v1/") ||
			strings.HasPrefix(r.URL.Path, "/webhooks/") ||
			strings.HasPrefix(r.URL.Path, "/webhook/") ||
			r.URL.Path == "/mcp" ||
			strings.HasPrefix(r.URL.Path, "/mcp/") ||
			r.URL.Path == "/healthz" ||
			r.URL.Path == "/metrics" {
			http.NotFound(w, r)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Serve real files (hashed assets, favicon, etc.). Do not pass
		// "/index.html" through FileServer: it 301s that path to "./",
		// and the browser resolves "./" against /jobs/123 → /jobs/.
		if path != "index.html" {
			if f, err := sub.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: keep the request URL so client routes survive refresh.
		serveIndex(w, r, sub)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	f, err := fsys.Open("index.html")
	if err != nil {
		http.Error(w, "dashboard index missing; run: make build-ui", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "dashboard index not seekable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, rs)
}
