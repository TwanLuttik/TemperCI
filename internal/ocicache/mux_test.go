package ocicache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMux_RoutesRegistryHostToOCI(t *testing.T) {
	mux := Mux(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "oci")
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "actions")
		}),
	)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://ghcr.io/v2/", nil)
	req.Host = "ghcr.io"
	mux.ServeHTTP(rr, req)
	if rr.Body.String() != "oci" {
		t.Fatalf("got %q", rr.Body.String())
	}
}

func TestMux_RoutesActionsHostToFallback(t *testing.T) {
	mux := Mux(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "oci")
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "actions")
		}),
	)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://results-receiver.actions.githubusercontent.com/twirp/x", nil)
	req.Host = "results-receiver.actions.githubusercontent.com"
	mux.ServeHTTP(rr, req)
	if rr.Body.String() != "actions" {
		t.Fatalf("got %q", rr.Body.String())
	}
}

func TestShouldTerminate(t *testing.T) {
	if !ShouldTerminate("ghcr.io") || !ShouldTerminate("registry-1.docker.io") {
		t.Fatal("registry hosts must terminate")
	}
	if !ShouldTerminate("results-receiver.actions.githubusercontent.com") {
		t.Fatal("actions cache hosts must still terminate")
	}
	if ShouldTerminate("auth.docker.io") {
		t.Fatal("auth.docker.io must splice")
	}
}
