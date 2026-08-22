package ghacache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const twirpPrefix = "/twirp/github.actions.results.api.v1.CacheService/"

// Counters are per-repo cache stats for the most recent job traffic.
type Counters struct {
	Hits     int
	Misses   int
	BytesIn  int64
	BytesOut int64
}

// Gateway terminates Actions cache v2 Twirp + blob transfer against a Store.
type Gateway struct {
	Store *Store
	// Proxy handles non-cache paths on intercepted hosts (ArtifactService, etc.).
	// Nil uses a TLS reverse proxy to https://<request Host>.
	Proxy http.Handler
	mu    sync.Mutex
	binds map[string]string // remote host → repo
	stats map[string]*Counters
}

// NewGateway wraps store. Store must be non-nil.
func NewGateway(store *Store) *Gateway {
	return &Gateway{
		Store: store,
		binds: make(map[string]string),
		stats: make(map[string]*Counters),
	}
}

// BindRemote associates a guest/source IP with org/repo.
func (g *Gateway) BindRemote(remote, repo string) {
	if g == nil {
		return
	}
	host := stripPort(remote)
	g.mu.Lock()
	g.binds[host] = repo
	if _, ok := g.stats[repo]; !ok {
		g.stats[repo] = &Counters{}
	}
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

// TakeStats returns and resets counters for repo.
func (g *Gateway) TakeStats(repo string) Counters {
	if g == nil {
		return Counters{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.stats[repo]
	if c == nil {
		return Counters{}
	}
	out := *c
	g.stats[repo] = &Counters{}
	return out
}

// Handler returns the HTTP handler (Twirp + blob). Non-cache paths are
// proxied to the real origin so upload-artifact (ArtifactService) still works
// on the same results-receiver host we MITM for cache.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+twirpPrefix+"CreateCacheEntry", g.handleCreate)
	mux.HandleFunc("POST "+twirpPrefix+"FinalizeCacheEntry", g.handleFinalize)
	mux.HandleFunc("POST "+twirpPrefix+"FinalizeCacheEntryUpload", g.handleFinalize)
	mux.HandleFunc("POST "+twirpPrefix+"GetCacheEntryDownloadURL", g.handleGet)
	mux.HandleFunc("/blob/", g.handleBlob)
	mux.HandleFunc("/c/", g.handleAzureBlob)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if localCachePath(r.URL.Path) {
			mux.ServeHTTP(w, r)
			return
		}
		g.serveNonCache(w, r)
	})
}

func localCachePath(path string) bool {
	return strings.HasPrefix(path, twirpPrefix) || strings.HasPrefix(path, "/blob/") || strings.HasPrefix(path, "/c/")
}

func (g *Gateway) serveNonCache(w http.ResponseWriter, r *http.Request) {
	if g.Proxy != nil {
		g.Proxy.ServeHTTP(w, r)
		return
	}
	host := r.Host
	if host == "" && r.TLS != nil {
		host = r.TLS.ServerName
	}
	if host == "" {
		http.Error(w, "no origin host", http.StatusBadGateway)
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "https", Host: host})
		},
	}
	rp.ServeHTTP(w, r)
}

// ListenAndServe serves the gateway on addr.
func (g *Gateway) ListenAndServe(addr string) error {
	srv := &http.Server{Addr: addr, Handler: g.Handler()}
	return srv.ListenAndServe()
}

func (g *Gateway) handleCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := g.repoOf(r)
	if !ok {
		http.Error(w, `{"ok":false,"msg":"no repo bind"}`, http.StatusForbidden)
		return
	}
	var req struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, `{"ok":false}`, http.StatusBadRequest)
		return
	}
	up, err := g.Store.Create(repo, req.Key, req.Version)
	if errors.Is(err, ErrExists) {
		writeJSON(w, map[string]any{"ok": false, "signed_upload_url": "", "signedUploadUrl": ""})
		return
	}
	if err != nil {
		http.Error(w, `{"ok":false}`, http.StatusInternalServerError)
		return
	}
	url := blobURL(r, "u/"+up.ID)
	writeJSON(w, map[string]any{"ok": true, "signed_upload_url": url, "signedUploadUrl": url})
}

func (g *Gateway) handleFinalize(w http.ResponseWriter, r *http.Request) {
	repo, ok := g.repoOf(r)
	if !ok {
		http.Error(w, `{"ok":false}`, http.StatusForbidden)
		return
	}
	var req struct {
		Key       string          `json:"key"`
		Version   string          `json:"version"`
		SizeBytes json.RawMessage `json:"size_bytes"`
		SizeCamel json.RawMessage `json:"sizeBytes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, `{"ok":false}`, http.StatusBadRequest)
		return
	}
	size := parseSize(req.SizeBytes)
	if size == 0 {
		size = parseSize(req.SizeCamel)
	}
	e, err := g.Store.Finalize(repo, req.Key, req.Version, size)
	if err != nil {
		http.Error(w, `{"ok":false}`, http.StatusInternalServerError)
		return
	}
	g.addBytesIn(repo, e.Size)
	id := numericEntryID(e.ID)
	writeJSON(w, map[string]any{"ok": true, "entry_id": id, "entryId": id})
}

func (g *Gateway) handleGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := g.repoOf(r)
	if !ok {
		http.Error(w, `{"ok":false}`, http.StatusForbidden)
		return
	}
	var req struct {
		Key         string   `json:"key"`
		Version     string   `json:"version"`
		RestoreKeys []string `json:"restore_keys"`
		RestoreCam  []string `json:"restoreKeys"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, `{"ok":false}`, http.StatusBadRequest)
		return
	}
	restore := req.RestoreKeys
	if len(restore) == 0 {
		restore = req.RestoreCam
	}
	e, hit, err := g.Store.Get(repo, req.Key, restore, req.Version)
	if err != nil {
		http.Error(w, `{"ok":false}`, http.StatusInternalServerError)
		return
	}
	if !hit {
		g.addMiss(repo)
		writeJSON(w, map[string]any{"ok": false})
		return
	}
	g.addHit(repo, e.Size)
	url := blobURL(r, "e/"+e.ID)
	writeJSON(w, map[string]any{
		"ok":                  true,
		"matched_key":         e.Key,
		"matchedKey":          e.Key,
		"signed_download_url": url,
		"signedDownloadUrl":   url,
	})
}

func (g *Gateway) handleBlob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/blob/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		g.handleBlobPut(w, r, id)
	case http.MethodGet, http.MethodHead:
		g.handleBlobGet(w, r, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleBlobPut(w http.ResponseWriter, r *http.Request, id string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<30))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	kind, rest, _ := strings.Cut(id, "/")
	q := r.URL.Query()
	switch kind {
	case "u":
		if q.Get("comp") == "block" {
			blockID := q.Get("blockid")
			if blockID == "" {
				http.Error(w, "blockid", http.StatusBadRequest)
				return
			}
			if err := g.Store.AppendUpload(rest, blockID, body); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			writeAzureCreated(w)
			return
		}
		if q.Get("comp") == "blocklist" {
			ids := parseBlockList(body)
			if err := g.Store.CommitBlocks(rest, ids); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeAzureCreated(w)
			return
		}
		if err := g.Store.WriteUpload(rest, body); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeAzureCreated(w)
	default:
		http.NotFound(w, r)
	}
}

func (g *Gateway) handleAzureBlob(w http.ResponseWriter, r *http.Request) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/blob/" + strings.TrimPrefix(r.URL.Path, "/c/")
	g.handleBlob(w, r2)
}

func (g *Gateway) handleBlobGet(w http.ResponseWriter, r *http.Request, id string) {
	kind, rest, _ := strings.Cut(id, "/")
	if kind != "e" {
		http.NotFound(w, r)
		return
	}
	f, err := g.Store.OpenBlob(rest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "stat", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, rest, st.ModTime(), f)
}

func (g *Gateway) repoOf(r *http.Request) (string, bool) {
	host := stripPort(r.RemoteAddr)
	g.mu.Lock()
	defer g.mu.Unlock()
	repo, ok := g.binds[host]
	return repo, ok && repo != ""
}

func (g *Gateway) addHit(repo string, size int64) {
	g.mu.Lock()
	c := g.ensureStats(repo)
	c.Hits++
	c.BytesOut += size
	g.mu.Unlock()
}

func (g *Gateway) addMiss(repo string) {
	g.mu.Lock()
	g.ensureStats(repo).Misses++
	g.mu.Unlock()
}

func (g *Gateway) addBytesIn(repo string, size int64) {
	g.mu.Lock()
	g.ensureStats(repo).BytesIn += size
	g.mu.Unlock()
}

func (g *Gateway) ensureStats(repo string) *Counters {
	c := g.stats[repo]
	if c == nil {
		c = &Counters{}
		g.stats[repo] = c
	}
	return c
}

// cacheBlobHost is the fake Azure account the guest resolves via /etc/hosts.
// The Azure SDK in actions/cache requires a *.blob.core.windows.net URL.
const cacheBlobHost = "tempercicache.blob.core.windows.net"

func blobURL(r *http.Request, suffix string) string {
	_ = r
	return "https://" + cacheBlobHost + "/c/" + suffix
}

func writeAzureCreated(w http.ResponseWriter) {
	w.Header().Set("ETag", `"0"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.Header().Set("x-ms-request-id", "temperci")
	w.Header().Set("x-ms-version", "2020-10-02")
	w.WriteHeader(http.StatusCreated)
}

func numericEntryID(s string) int64 {
	sum := sha256.Sum256([]byte(s))
	v := int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff)
	if v == 0 {
		return 1
	}
	return v
}

func parseSize(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func parseBlockList(body []byte) []string {
	// Azure XML: <Latest>blockid</Latest>
	s := string(body)
	var ids []string
	for {
		i := strings.Index(s, "<Latest>")
		if i < 0 {
			break
		}
		s = s[i+8:]
		j := strings.Index(s, "</Latest>")
		if j < 0 {
			break
		}
		ids = append(ids, s[:j])
		s = s[j+9:]
	}
	return ids
}
