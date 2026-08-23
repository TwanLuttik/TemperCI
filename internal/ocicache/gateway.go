package ocicache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Gateway is a Registry API v2 pull-through + local build-cache endpoint.
type Gateway struct {
	Store *Store
	// Origin is used for upstream registry requests. Nil uses http.DefaultTransport.
	Origin http.RoundTripper
	// OriginBase, when set, rewrites upstream requests to this base URL (tests).
	OriginBase string
	// TokenSource mints an anonymous pull token. Nil uses Docker Hub's token service
	// for registry-1.docker.io and no token for GHCR.
	TokenSource func(host, name string) (string, error)

	mu      sync.Mutex
	binds   map[string]string
	uploads map[string]*uploadSession
}

type uploadSession struct {
	Name string
	Repo string
	Path string
}

// NewGateway wraps store. Store must be non-nil.
func NewGateway(store *Store) *Gateway {
	return &Gateway{
		Store:   store,
		binds:   make(map[string]string),
		uploads: make(map[string]*uploadSession),
	}
}

// BindRemote associates a guest/source IP with org/repo.
func (g *Gateway) BindRemote(remote, repo string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.binds[stripPort(remote)] = repo
	g.mu.Unlock()
}

// UnbindRemote drops the guest IP mapping.
func (g *Gateway) UnbindRemote(remote string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.binds, stripPort(remote))
	g.mu.Unlock()
}

func (g *Gateway) repoOf(r *http.Request) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	repo, ok := g.binds[stripPort(r.RemoteAddr)]
	return repo, ok && repo != ""
}

// Handler serves Registry API v2.
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(g.serve)
}

func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	kind, name, extra, ok := parseRegistryPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch kind {
	case "base":
		w.WriteHeader(http.StatusOK)
		return
	case "manifest":
		g.serveManifest(w, r, name, extra)
	case "blob":
		g.serveBlob(w, r, name, extra)
	case "upload":
		g.serveUpload(w, r, name, extra)
	case "tags":
		g.proxy(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g *Gateway) serveManifest(w http.ResponseWriter, r *http.Request, name, ref string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		g.getManifest(w, r, name, ref)
	case http.MethodPut:
		if !IsBuildCacheName(name) {
			g.proxy(w, r)
			return
		}
		g.putBuildManifest(w, r, name, ref)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) getManifest(w http.ResponseWriter, r *http.Request, name, ref string) {
	repo, _ := g.repoOf(r)
	if m, ok, err := g.Store.FindManifest(repo, hostOf(r), name, ref); err == nil && ok {
		writeManifest(w, r, m)
		return
	}
	status, hdr, body, scope, err := g.fetchOrigin(r, name)
	if err != nil || status >= 500 || status == 0 {
		if m, ok, _ := g.Store.FindManifest(repo, hostOf(r), name, ref); ok {
			writeManifest(w, r, m)
			return
		}
		if err != nil {
			http.Error(w, "origin fetch failed", http.StatusBadGateway)
			return
		}
	}
	if status != http.StatusOK {
		copyHeader(w.Header(), hdr)
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	if len(body) == 0 {
		http.Error(w, "empty origin manifest", http.StatusBadGateway)
		return
	}
	digest := hdr.Get("Docker-Content-Digest")
	if digest == "" {
		sum := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	media := hdr.Get("Content-Type")
	if scope == ScopeRepo && repo == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	m := Manifest{
		Host:      hostOf(r),
		Name:      name,
		Reference: ref,
		MediaType: media,
		Digest:    digest,
		Body:      body,
		Scope:     scope,
		Repo:      repo,
	}
	_ = g.Store.PutManifest(m)
	writeManifest(w, r, m)
}

func (g *Gateway) putBuildManifest(w http.ResponseWriter, r *http.Request, name, ref string) {
	repo, ok := g.repoOf(r)
	if !ok {
		http.Error(w, "no repo bind", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	media := r.Header.Get("Content-Type")
	if media == "" {
		media = "application/vnd.oci.image.manifest.v1+json"
	}
	m := Manifest{
		Host:      hostOf(r),
		Name:      name,
		Reference: ref,
		MediaType: media,
		Digest:    digest,
		Body:      body,
		Scope:     ScopeRepo,
		Repo:      repo,
	}
	if err := g.Store.PutManifest(m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Location", "/v2/"+name+"/manifests/"+ref)
	w.WriteHeader(http.StatusCreated)
}

func (g *Gateway) serveBlob(w http.ResponseWriter, r *http.Request, name, digest string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	repo, _ := g.repoOf(r)
	if scope, ok := g.Store.FindBlob(repo, digest); ok {
		g.writeBlob(w, r, scope, repo, digest)
		return
	}
	status, hdr, rc, scope, err := g.fetchOriginStream(r, name)
	if rc != nil {
		defer rc.Close()
	}
	if err != nil || status >= 500 || status == 0 {
		if scope, ok := g.Store.FindBlob(repo, digest); ok {
			g.writeBlob(w, r, scope, repo, digest)
			return
		}
		if err != nil {
			http.Error(w, "origin fetch failed", http.StatusBadGateway)
			return
		}
	}
	if status != http.StatusOK {
		copyHeader(w.Header(), hdr)
		w.WriteHeader(status)
		if r.Method != http.MethodHead && rc != nil {
			_, _ = io.Copy(w, rc)
		}
		return
	}
	if scope == ScopeRepo && repo == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	if cl := hdr.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	tmp, err := os.CreateTemp("", "temperci-oci-blob-*")
	if err != nil {
		http.Error(w, "cache temp", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(io.MultiWriter(w, tmp), io.LimitReader(rc, 2<<30)); err != nil {
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err == nil {
		_ = g.Store.PutBlobFromReader(scope, repo, digest, tmp)
	}
}

func (g *Gateway) writeBlob(w http.ResponseWriter, r *http.Request, scope Scope, repo, digest string) {
	if scope == ScopePublic {
		repo = ""
	}
	f, size, err := g.Store.OpenBlob(scope, repo, digest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = io.Copy(w, f)
}

func (g *Gateway) serveUpload(w http.ResponseWriter, r *http.Request, name, uuid string) {
	if !IsBuildCacheName(name) {
		g.proxy(w, r)
		return
	}
	repo, ok := g.repoOf(r)
	if !ok {
		http.Error(w, "no repo bind", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPost:
		id, err := randomID()
		if err != nil {
			http.Error(w, "id", http.StatusInternalServerError)
			return
		}
		tmp, err := os.CreateTemp("", "temperci-oci-up-*.tmp")
		if err != nil {
			http.Error(w, "tmp", http.StatusInternalServerError)
			return
		}
		path := tmp.Name()
		_ = tmp.Close()
		g.mu.Lock()
		g.uploads[id] = &uploadSession{Name: name, Repo: repo, Path: path}
		g.mu.Unlock()
		loc := "/v2/" + name + "/blobs/uploads/" + id
		w.Header().Set("Location", loc)
		w.Header().Set("Docker-Upload-Uuid", id)
		w.Header().Set("Range", "0-0")
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPatch:
		g.mu.Lock()
		up, ok := g.uploads[uuid]
		g.mu.Unlock()
		if !ok || up.Path == "" {
			http.NotFound(w, r)
			return
		}
		f, err := os.OpenFile(up.Path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, "open", http.StatusInternalServerError)
			return
		}
		n, err := io.Copy(f, io.LimitReader(r.Body, 2<<30))
		_ = f.Close()
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		st, _ := os.Stat(up.Path)
		end := int64(0)
		if st != nil {
			end = st.Size() - 1
		}
		_ = n
		w.Header().Set("Location", "/v2/"+name+"/blobs/uploads/"+uuid)
		w.Header().Set("Docker-Upload-Uuid", uuid)
		w.Header().Set("Range", fmt.Sprintf("0-%d", end))
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		g.mu.Lock()
		up, ok := g.uploads[uuid]
		if ok {
			delete(g.uploads, uuid)
		}
		g.mu.Unlock()
		if !ok || up.Path == "" {
			http.NotFound(w, r)
			return
		}
		defer os.Remove(up.Path)
		f, err := os.OpenFile(up.Path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, "open", http.StatusInternalServerError)
			return
		}
		_, _ = io.Copy(f, io.LimitReader(r.Body, 2<<30))
		_ = f.Close()
		digest := r.URL.Query().Get("digest")
		if digest == "" {
			http.Error(w, "digest required", http.StatusBadRequest)
			return
		}
		in, err := os.Open(up.Path)
		if err != nil {
			http.Error(w, "open", http.StatusInternalServerError)
			return
		}
		err = g.Store.PutBlobFromReader(ScopeRepo, repo, digest, in)
		_ = in.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
		w.Header().Set("Location", "/v2/"+name+"/blobs/"+digest)
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) fetchOrigin(r *http.Request, name string) (status int, hdr http.Header, body []byte, scope Scope, err error) {
	status, hdr, rc, scope, err := g.fetchOriginStream(r, name)
	if rc != nil {
		defer rc.Close()
	}
	if err != nil {
		return 0, nil, nil, "", err
	}
	b, rerr := io.ReadAll(io.LimitReader(rc, 2<<30))
	return status, hdr, b, scope, rerr
}

func (g *Gateway) fetchOriginStream(r *http.Request, name string) (status int, hdr http.Header, body io.ReadCloser, scope Scope, err error) {
	host := hostOf(r)
	anon, err := g.doOrigin(r, name, host, "")
	if err != nil {
		return 0, nil, nil, "", err
	}
	if anon.StatusCode == http.StatusOK {
		return anon.StatusCode, anon.Header, anon.Body, ScopePublic, nil
	}
	guestAuth := r.Header.Get("Authorization")
	repo, bound := g.repoOf(r)
	if (anon.StatusCode == http.StatusUnauthorized || anon.StatusCode == http.StatusForbidden) && guestAuth != "" && bound && repo != "" {
		_, _ = io.Copy(io.Discard, anon.Body)
		_ = anon.Body.Close()
		priv, err := g.doOrigin(r, name, host, guestAuth)
		if err != nil {
			return 0, nil, nil, "", err
		}
		return priv.StatusCode, priv.Header, priv.Body, ScopeRepo, nil
	}
	return anon.StatusCode, anon.Header, anon.Body, ScopePublic, nil
}

func (g *Gateway) doOrigin(r *http.Request, name, host, authorization string) (*http.Response, error) {
	// Always GET from origin so HEAD probes do not cache an empty body.
	out, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://"+host+r.URL.Path, nil)
	if err != nil {
		return nil, err
	}
	out.URL.RawQuery = r.URL.RawQuery
	if g.OriginBase != "" {
		base, err := url.Parse(g.OriginBase)
		if err != nil {
			return nil, err
		}
		out.URL = base.ResolveReference(&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery})
		out.Host = base.Host
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		out.Header.Set("Accept", accept)
	}
	if authorization != "" {
		out.Header.Set("Authorization", authorization)
	} else if tok := g.anonymousToken(host, name); tok != "" {
		out.Header.Set("Authorization", "Bearer "+tok)
	}
	// http.Client follows Hub/GHCR 307s to the blob CDN. RoundTrip does not,
	// which would leak Location to the guest and skip caching the layer.
	cli := &http.Client{Transport: g.roundTrip()}
	return cli.Do(out)
}

func (g *Gateway) anonymousToken(host, name string) string {
	fn := g.TokenSource
	if fn == nil {
		fn = defaultAnonymousToken
	}
	tok, err := fn(host, name)
	if err != nil {
		return ""
	}
	return tok
}

func (g *Gateway) proxy(w http.ResponseWriter, r *http.Request) {
	host := hostOf(r)
	var body io.Reader = r.Body
	out, err := http.NewRequestWithContext(r.Context(), r.Method, "https://"+host+r.URL.RequestURI(), body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	if g.OriginBase != "" {
		base, err := url.Parse(g.OriginBase)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out.URL = base.ResolveReference(&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery})
		out.Host = base.Host
	}
	resp, err := g.roundTrip().RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g *Gateway) roundTrip() http.RoundTripper {
	if g.Origin != nil {
		return g.Origin
	}
	return http.DefaultTransport
}

func writeManifest(w http.ResponseWriter, r *http.Request, m Manifest) {
	if m.MediaType != "" {
		w.Header().Set("Content-Type", m.MediaType)
	}
	w.Header().Set("Docker-Content-Digest", m.Digest)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(m.Body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(m.Body)
}

func parseRegistryPath(path string) (kind, name, extra string, ok bool) {
	p := strings.TrimPrefix(path, "/")
	if p != "v2" && !strings.HasPrefix(p, "v2/") {
		return "", "", "", false
	}
	if p == "v2" || p == "v2/" {
		return "base", "", "", true
	}
	rest := strings.TrimPrefix(p, "v2/")
	if strings.HasSuffix(rest, "/tags/list") {
		return "tags", strings.TrimSuffix(rest, "/tags/list"), "", true
	}
	if i := strings.Index(rest, "/manifests/"); i >= 0 {
		return "manifest", rest[:i], rest[i+len("/manifests/"):], true
	}
	if i := strings.Index(rest, "/blobs/uploads"); i >= 0 {
		extra = strings.TrimPrefix(rest[i+len("/blobs/uploads"):], "/")
		return "upload", rest[:i], extra, true
	}
	if i := strings.Index(rest, "/blobs/"); i >= 0 {
		return "blob", rest[:i], rest[i+len("/blobs/"):], true
	}
	return "", "", "", false
}

func hostOf(r *http.Request) string {
	h := r.Host
	if h == "" && r.TLS != nil {
		h = r.TLS.ServerName
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		// keep IPv6
		if !strings.Contains(h, "]") {
			h = h[:i]
		}
	}
	return strings.ToLower(h)
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		if strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
