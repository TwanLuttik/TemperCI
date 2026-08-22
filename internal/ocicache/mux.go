package ocicache

import (
	"net/http"
	"strings"
)

// Mux routes Hub/GHCR requests to oci and everything else to fallback
// (the Actions cache gateway).
func Mux(oci, fallback http.Handler) http.Handler {
	if oci == nil {
		oci = http.NotFoundHandler()
	}
	if fallback == nil {
		fallback = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if host == "" && r.TLS != nil {
			host = r.TLS.ServerName
		}
		if i := strings.IndexByte(host, ':'); i >= 0 && !strings.Contains(host, "]") {
			host = host[:i]
		}
		if IsRegistryHost(host) {
			oci.ServeHTTP(w, r)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}
