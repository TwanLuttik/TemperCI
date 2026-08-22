package ocicache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGateway_V2Base(t *testing.T) {
	g := newTestGateway(t, nil)
	rr := do(g, http.MethodGet, "https://ghcr.io/v2/", "", "")
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	if rr.Header().Get("Docker-Distribution-Api-Version") != "registry/2.0" {
		t.Fatalf("api version=%q", rr.Header().Get("Docker-Distribution-Api-Version"))
	}
}

func TestGateway_PublicPullThroughThenHit(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		if r.Header.Get("Authorization") == "Bearer guest-token" {
			t.Errorf("public fetch must not forward guest Authorization")
		}
		if r.URL.Path != "/v2/library/postgres/manifests/16" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(body)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.BindRemote("192.0.2.10", "acme/app")

	req := httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/library/postgres/manifests/16", nil)
	req.Host = "registry-1.docker.io"
	req.RemoteAddr = "192.0.2.10:9"
	req.Header.Set("Authorization", "Bearer guest-token")
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("first code=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Docker-Content-Digest") != digest {
		t.Fatalf("digest=%q", rr.Header().Get("Docker-Content-Digest"))
	}
	if originHits.Load() != 1 {
		t.Fatalf("origin hits=%d", originHits.Load())
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/library/postgres/manifests/16", nil)
	req2.Host = "registry-1.docker.io"
	req2.RemoteAddr = "192.0.2.10:9"
	g.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != 200 || rr2.Body.String() != string(body) {
		t.Fatalf("hit code=%d body=%s", rr2.Code, rr2.Body.String())
	}
	if originHits.Load() != 1 {
		t.Fatalf("second GET must be a cache hit, origin=%d", originHits.Load())
	}
}

func TestGateway_FollowsBlobRedirectAndCaches(t *testing.T) {
	layer := []byte("layer-bytes-from-cdn")
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	var cdnHits, originHits atomic.Int32

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(layer)
	}))
	t.Cleanup(cdn.Close)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		if r.Header.Get("Authorization") == "" {
			t.Error("registry request must carry anonymous token")
		}
		w.Header().Set("Location", cdn.URL+"/data")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.BindRemote("192.0.2.10", "acme/app")

	req := httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/library/redis/blobs/"+digest, nil)
	req.Host = "registry-1.docker.io"
	req.RemoteAddr = "192.0.2.10:9"
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("first code=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != string(layer) {
		t.Fatalf("body=%q", rr.Body.String())
	}
	if rr.Header().Get("Location") != "" {
		t.Fatalf("guest must not see CDN redirect, Location=%q", rr.Header().Get("Location"))
	}
	if originHits.Load() != 1 || cdnHits.Load() != 1 {
		t.Fatalf("origin=%d cdn=%d", originHits.Load(), cdnHits.Load())
	}
	if _, ok := g.Store.FindBlob("acme/app", digest); !ok {
		t.Fatal("blob must be cached after following the CDN redirect")
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/library/redis/blobs/"+digest, nil)
	req2.Host = "registry-1.docker.io"
	req2.RemoteAddr = "192.0.2.10:9"
	g.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != 200 || rr2.Body.String() != string(layer) {
		t.Fatalf("hit code=%d body=%s", rr2.Code, rr2.Body.String())
	}
	if originHits.Load() != 1 || cdnHits.Load() != 1 {
		t.Fatalf("second GET must be a cache hit origin=%d cdn=%d", originHits.Load(), cdnHits.Load())
	}
}

func TestGateway_HEADDoesNotCacheEmptyManifest(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json"}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.list.v2+json")
		w.Header().Set("Docker-Content-Digest", digest)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.BindRemote("192.0.2.10", "acme/app")

	req := httptest.NewRequest(http.MethodHead, "https://registry-1.docker.io/v2/library/redis/manifests/7", nil)
	req.Host = "registry-1.docker.io"
	req.RemoteAddr = "192.0.2.10:9"
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("HEAD code=%d", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/library/redis/manifests/7", nil)
	req2.Host = "registry-1.docker.io"
	req2.RemoteAddr = "192.0.2.10:9"
	rr2 := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != 200 || rr2.Body.String() != string(body) {
		t.Fatalf("GET after HEAD code=%d body=%q", rr2.Code, rr2.Body.String())
	}
}

func TestGateway_PrivateBlobIsolatedByRepo(t *testing.T) {
	layer := []byte("secret-layer-bytes")
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(layer)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.TokenSource = func(host, name string) (string, error) { return "", nil }
	g.BindRemote("192.0.2.10", "acme/app")
	g.BindRemote("192.0.2.11", "other/app")

	req := httptest.NewRequest(http.MethodGet, "https://ghcr.io/v2/acme/private/blobs/"+digest, nil)
	req.Host = "ghcr.io"
	req.RemoteAddr = "192.0.2.10:9"
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("owner fetch code=%d", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "https://ghcr.io/v2/acme/private/blobs/"+digest, nil)
	req2.Host = "ghcr.io"
	req2.RemoteAddr = "192.0.2.11:9"
	rr2 := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr2, req2)
	// other repo retries origin without matching guest token → 401, not the cached secret
	if rr2.Code == 200 {
		t.Fatal("other repo must not receive private blob from cache")
	}
}

func TestGateway_UnboundPrivateNotCached(t *testing.T) {
	layer := []byte("nope")
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(layer)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.TokenSource = func(host, name string) (string, error) { return "", nil }

	req := httptest.NewRequest(http.MethodGet, "https://ghcr.io/v2/acme/private/blobs/"+digest, nil)
	req.Host = "ghcr.io"
	req.RemoteAddr = "192.0.2.99:9"
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("unbound private want 401/403, got %d", rr.Code)
	}
	if _, ok := g.Store.FindBlob("acme/app", digest); ok {
		t.Fatal("must not cache unbound private")
	}
}

func TestGateway_BuildCachePutNotForwarded(t *testing.T) {
	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		t.Errorf("origin must not see build-cache write %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.BindRemote("192.0.2.10", "acme/app")

	layer := []byte("build-layer")
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	name := "__temperci_cache/acme/app/buildkit"

	// Start upload
	req := httptest.NewRequest(http.MethodPost, "https://ghcr.io/v2/"+name+"/blobs/uploads/", nil)
	req.Host = "ghcr.io"
	req.RemoteAddr = "192.0.2.10:9"
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start upload code=%d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc == "" {
		t.Fatal("missing Location")
	}

	putURL := loc
	if !strings.Contains(putURL, "digest=") {
		if strings.Contains(putURL, "?") {
			putURL += "&digest=" + digest
		} else {
			putURL += "?digest=" + digest
		}
	}
	req2 := httptest.NewRequest(http.MethodPut, putURL, bytes.NewReader(layer))
	req2.Host = "ghcr.io"
	req2.RemoteAddr = "192.0.2.10:9"
	rr2 := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusCreated {
		t.Fatalf("commit code=%d body=%s", rr2.Code, rr2.Body.String())
	}

	man := []byte(`{"schemaVersion":2}`)
	manSum := sha256.Sum256(man)
	manDigest := "sha256:" + hex.EncodeToString(manSum[:])
	req3 := httptest.NewRequest(http.MethodPut, "https://ghcr.io/v2/"+name+"/manifests/latest", bytes.NewReader(man))
	req3.Host = "ghcr.io"
	req3.RemoteAddr = "192.0.2.10:9"
	req3.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	rr3 := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusCreated {
		t.Fatalf("manifest put code=%d", rr3.Code)
	}

	req4 := httptest.NewRequest(http.MethodGet, "https://ghcr.io/v2/"+name+"/manifests/latest", nil)
	req4.Host = "ghcr.io"
	req4.RemoteAddr = "192.0.2.10:9"
	rr4 := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr4, req4)
	if rr4.Code != 200 || rr4.Body.String() != string(man) {
		t.Fatalf("get build cache code=%d body=%s", rr4.Code, rr4.Body.String())
	}
	if got := rr4.Header().Get("Docker-Content-Digest"); got != manDigest {
		t.Fatalf("manifest digest=%q", got)
	}
	if originHits.Load() != 0 {
		t.Fatalf("origin hits=%d", originHits.Load())
	}
	_ = digest
}

func TestGateway_RealPushProxied(t *testing.T) {
	var gotPath string
	var gotBody string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(origin.Close)

	g := newTestGateway(t, origin)
	g.BindRemote("192.0.2.10", "acme/app")
	body := []byte(`{"schemaVersion":2}`)
	req := httptest.NewRequest(http.MethodPut, "https://ghcr.io/v2/acme/app/manifests/v1", bytes.NewReader(body))
	req.Host = "ghcr.io"
	req.RemoteAddr = "192.0.2.10:9"
	req.Header.Set("Authorization", "Bearer push-token")
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d", rr.Code)
	}
	if gotPath != "/v2/acme/app/manifests/v1" {
		t.Fatalf("proxied path=%q", gotPath)
	}
	if gotBody != string(body) {
		t.Fatalf("proxied body=%q", gotBody)
	}
}

func TestGateway_StaleOnOriginFailure(t *testing.T) {
	body := []byte(`{"schemaVersion":2}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	s := openTestStore(t, 1<<20)
	if err := s.PutManifest(Manifest{
		Host: "registry-1.docker.io", Name: "library/postgres", Reference: "16",
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    digest, Body: body, Scope: ScopePublic,
	}); err != nil {
		t.Fatal(err)
	}
	g := NewGateway(s)
	g.Origin = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})
	req := httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/library/postgres/manifests/16", nil)
	req.Host = "registry-1.docker.io"
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 || rr.Body.String() != string(body) {
		t.Fatalf("stale code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func newTestGateway(t *testing.T, origin *httptest.Server) *Gateway {
	t.Helper()
	g := NewGateway(openTestStore(t, 1<<20))
	if origin != nil {
		g.Origin = origin.Client().Transport
		g.OriginBase = origin.URL
	}
	g.TokenSource = func(host, name string) (string, error) {
		return "anon-token", nil
	}
	return g
}

func do(g *Gateway, method, url, remote, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	if remote != "" {
		req.RemoteAddr = remote
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	g.Handler().ServeHTTP(rr, req)
	return rr
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
