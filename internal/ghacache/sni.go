package ghacache

import "strings"

// ShouldIntercept reports whether TLS SNI for host should terminate at the
// local Actions cache gateway instead of being spliced to the real destination.
func ShouldIntercept(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return false
	}
	// Cache Twirp lives on the Results API host.
	if strings.HasSuffix(h, ".actions.githubusercontent.com") || h == "actions.githubusercontent.com" {
		return strings.Contains(h, "result")
	}
	// Azure SDK uploads require a *.blob.core.windows.net URL. We mint
	// tempercicache.blob.core.windows.net; real Actions Azure accounts stay spliced.
	if h == cacheBlobHost {
		return true
	}
	return false
}
