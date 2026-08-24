package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAHandler_JobDetailDoesNotRedirect(t *testing.T) {
	h := SPAHandler()
	req := httptest.NewRequest(http.MethodGet, "/jobs/123456", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusMovedPermanently || rr.Code == http.StatusFound {
		t.Fatalf("job detail refresh redirected to %q (status %d)", rr.Header().Get("Location"), rr.Code)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<div id=\"root\">") {
		t.Fatalf("expected SPA index.html, got %q", body[:min(200, len(body))])
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q", ct)
	}
}

func TestSPAHandler_JobsListDoesNotRedirect(t *testing.T) {
	h := SPAHandler()
	for _, path := range []string{"/jobs", "/jobs/", "/hosts", "/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status=%d location=%q", path, rr.Code, rr.Header().Get("Location"))
		}
	}
}

func TestSPAHandler_MCPPathIsNotSPA(t *testing.T) {
	h := SPAHandler()
	for _, path := range []string{"/mcp", "/mcp/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d (SPA must not claim /mcp)", path, rr.Code)
		}
	}
}

func TestSPAHandler_AssetsStillServed(t *testing.T) {
	h := SPAHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body := rr.Body.String()
	i := strings.Index(body, `src="/assets/`)
	if i < 0 {
		t.Fatalf("index.html missing /assets script: %s", body)
	}
	rest := body[i+len(`src="`):]
	end := strings.IndexByte(rest, '"')
	asset := rest[:end]
	req = httptest.NewRequest(http.MethodGet, asset, nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("asset %s status=%d", asset, rr.Code)
	}
}
