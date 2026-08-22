// Package ocicache is a host-local OCI pull-through and BuildKit registry cache.
package ocicache

import (
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/ghacache"
)

const buildCachePrefix = "__temperci_cache/"

// IsRegistryHost reports whether SNI should terminate at the OCI gateway.
func IsRegistryHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	switch h {
	case "registry-1.docker.io", "ghcr.io":
		return true
	default:
		return false
	}
}

// IsBuildCacheName reports whether a registry repository name is the reserved
// local BuildKit cache namespace (never forwarded to origin).
func IsBuildCacheName(name string) bool {
	return strings.HasPrefix(name, buildCachePrefix)
}

// ShouldTerminate reports whether the SNI intercept should MITM this host
// (Actions cache hosts or Hub/GHCR registry API).
func ShouldTerminate(host string) bool {
	return IsRegistryHost(host) || ghacache.ShouldIntercept(host)
}
