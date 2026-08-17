package ghacache

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	dl, err := http.Get(got.URL)
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
