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
	if strings.HasSuffix(h, ".blob.core.windows.net") || h == "blob.core.windows.net" {
		return true
	}
	if strings.HasSuffix(h, ".actions.githubusercontent.com") || h == "actions.githubusercontent.com" {
		return strings.Contains(h, "result")
	}
	return false
}
