package ghacache

import "strings"

// BypassHosts are high-volume destinations that never need the cache MITM.
// Guest :443 to these IPs should skip the intercept DNAT.
func BypassHosts() []string {
	return []string{
		"github.com",
		"api.github.com",
		"codeload.github.com",
		"objects.githubusercontent.com",
		"github-releases.githubusercontent.com",
		"registry.npmjs.org",
		"registry.yarnpkg.com",
		"proxy.golang.org",
		"sum.golang.org",
		"goproxy.io",
		"nodejs.org",
		"pypi.org",
		"files.pythonhosted.org",
		"auth.docker.io",
	}
}

// ShouldBypass reports whether SNI should skip intercept (direct to origin).
func ShouldBypass(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	for _, b := range BypassHosts() {
		if h == b || strings.HasSuffix(h, "."+b) {
			return true
		}
	}
	return false
}

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
