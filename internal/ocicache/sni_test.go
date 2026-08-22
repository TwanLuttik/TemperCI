package ocicache

import "testing"

func TestIsRegistryHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"registry-1.docker.io", true},
		{"ghcr.io", true},
		{"GHCR.IO", true},
		{"ghcr.io:443", true},
		{"auth.docker.io", false},
		{"registry-1.docker.io.evil.example", false},
		{"api.github.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsRegistryHost(tc.host); got != tc.want {
			t.Fatalf("IsRegistryHost(%q)=%v want %v", tc.host, got, tc.want)
		}
	}
}

func TestIsBuildCacheName(t *testing.T) {
	if !IsBuildCacheName("__temperci_cache/acme/app/buildkit") {
		t.Fatal("expected build-cache name")
	}
	if IsBuildCacheName("library/postgres") || IsBuildCacheName("acme/app") {
		t.Fatal("real image must not be build-cache")
	}
	if IsBuildCacheName("") || IsBuildCacheName("__temperci_cache") {
		t.Fatal("prefix without slash is not a cache name")
	}
}
