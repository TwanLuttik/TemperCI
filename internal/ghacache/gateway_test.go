package ghacache

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGateway_CreateUploadDownload(t *testing.T) {
	st := openTestStore(t, 1<<20)
	g := NewGateway(st)
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)
	g.BindRemote("127.0.0.1", "acme/app")

	createBody := `{"key":"node-modules","version":"v1"}`
	resp, err := http.Post(ts.URL+twirpPrefix+"CreateCacheEntry", "application/json", bytes.NewBufferString(createBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created struct {
		OK  bool   `json:"ok"`
		URL string `json:"signed_upload_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.URL == "" {
		t.Fatalf("create = %+v", created)
	}

	payload := []byte("cache-payload")
	req, err := http.NewRequest(http.MethodPut, created.URL, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(ts.URL, "http://")
	req.Host = cacheBlobHost
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("put status=%d", putResp.StatusCode)
	}

	finBody := `{"key":"node-modules","version":"v1","size_bytes":13}`
	finResp, err := http.Post(ts.URL+twirpPrefix+"FinalizeCacheEntry", "application/json", bytes.NewBufferString(finBody))
	if err != nil {
		t.Fatal(err)
	}
	finResp.Body.Close()
	if finResp.StatusCode != http.StatusOK {
		t.Fatalf("finalize status=%d", finResp.StatusCode)
	}

	getBody := `{"key":"node-modules","version":"v1"}`
	getResp, err := http.Post(ts.URL+twirpPrefix+"GetCacheEntryDownloadURL", "application/json", bytes.NewBufferString(getBody))
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	var got struct {
		OK  bool   `json:"ok"`
		Key string `json:"matched_key"`
		URL string `json:"signed_download_url"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Key != "node-modules" || got.URL == "" {
		t.Fatalf("get = %+v", got)
	}
	dlReq, err := http.NewRequest(http.MethodGet, got.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	dlReq.URL.Scheme = "http"
	dlReq.URL.Host = strings.TrimPrefix(ts.URL, "http://")
	dlReq.Host = cacheBlobHost
	dl, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	data, _ := io.ReadAll(dl.Body)
	if string(data) != string(payload) {
		t.Fatalf("downloaded %q", data)
	}
	stt := g.TakeStats("acme/app")
	if stt.Hits != 1 || stt.Misses != 0 || stt.BytesIn != 13 {
		t.Fatalf("stats=%+v", stt)
	}
}

func TestGateway_ForbiddenWithoutBind(t *testing.T) {
	st := openTestStore(t, 1<<20)
	g := NewGateway(st)
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+twirpPrefix+"GetCacheEntryDownloadURL", "application/json", bytes.NewBufferString(`{"key":"k","version":"v"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestGateway_ArtifactServiceGoesToProxy(t *testing.T) {
	st := openTestStore(t, 1<<20)
	var gotPath string
	g := NewGateway(st)
	g.Proxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)

	path := "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact"
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if gotPath != path {
		t.Fatalf("proxied path=%q want %q", gotPath, path)
	}
}

func TestGateway_CacheServiceDoesNotHitProxy(t *testing.T) {
	st := openTestStore(t, 1<<20)
	g := NewGateway(st)
	g.BindRemote("127.0.0.1", "acme/app")
	g.Proxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("proxy should not see cache path %s", r.URL.Path)
	})
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+twirpPrefix+"CreateCacheEntry", "application/json", bytes.NewBufferString(`{"key":"k","version":"v"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestGateway_CreateReturnsAzureShapedURL(t *testing.T) {
	st := openTestStore(t, 1<<20)
	g := NewGateway(st)
	g.BindRemote("127.0.0.1", "acme/app")
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+twirpPrefix+"CreateCacheEntry", "application/json", bytes.NewBufferString(`{"key":"k","version":"v"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created struct {
		OK  bool   `json:"ok"`
		URL string `json:"signedUploadUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !created.OK {
		t.Fatal("create not ok")
	}
	if !strings.Contains(created.URL, "tempercicache.blob.core.windows.net") {
		t.Fatalf("url not azure-shaped: %s", created.URL)
	}
}

func TestGateway_FinalizeCacheEntryUploadAlias(t *testing.T) {
	st := openTestStore(t, 1<<20)
	g := NewGateway(st)
	g.BindRemote("127.0.0.1", "acme/app")
	h := g.Handler()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	cr, err := http.Post(ts.URL+twirpPrefix+"CreateCacheEntry", "application/json", bytes.NewBufferString(`{"key":"k","version":"v"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		OK  bool   `json:"ok"`
		URL string `json:"signed_upload_url"`
	}
	if err := json.NewDecoder(cr.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	cr.Body.Close()
	put, err := http.NewRequest(http.MethodPut, created.URL, bytes.NewReader([]byte("hello-cache")))
	if err != nil {
		t.Fatal(err)
	}
	// Azure SDK talks to tempercicache.blob.core.windows.net; serve via the same handler.
	put.URL.Scheme = "http"
	put.URL.Host = ts.Listener.Addr().String()
	put.Host = "tempercicache.blob.core.windows.net"
	pr, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	pr.Body.Close()
	if pr.StatusCode != http.StatusCreated {
		t.Fatalf("put status=%d", pr.StatusCode)
	}
	if pr.Header.Get("ETag") == "" {
		t.Fatal("missing ETag")
	}

	fin := `{"key":"k","version":"v","sizeBytes":"11"}`
	fr, err := http.Post(ts.URL+"/twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntryUpload", "application/json", bytes.NewBufferString(fin))
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Body.Close()
	if fr.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(fr.Body)
		t.Fatalf("finalize status=%d body=%s", fr.StatusCode, body)
	}
	var finResp struct {
		OK      bool    `json:"ok"`
		EntryID float64 `json:"entryId"`
	}
	if err := json.NewDecoder(fr.Body).Decode(&finResp); err != nil {
		t.Fatal(err)
	}
	if !finResp.OK || finResp.EntryID == 0 {
		t.Fatalf("finalize resp=%+v", finResp)
	}
}

func TestLocalCachePath_AzureContainer(t *testing.T) {
	if !localCachePath("/c/u/abc") {
		t.Fatal("expected /c/ azure path to be local")
	}
}
