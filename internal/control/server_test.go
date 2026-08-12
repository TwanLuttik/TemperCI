package control

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestServer_InvalidSignature_NoSideEffects(t *testing.T) {
	m := &mockMinter{}
	store := NewAssignmentStore()
	h := NewHandler(m, store, HandlerConfig{})
	srv := NewServer(ServerConfig{Handler: h, WebhookSecret: "super-secret"})

	body := fixture(t, "workflow_job_queued_temperci.json")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=00")
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if len(m.calls) != 0 {
		t.Fatalf("expected no JIT calls on bad signature, got %d", len(m.calls))
	}
	if store.Len() != 0 {
		t.Fatalf("expected no store side effects, got %d", store.Len())
	}
}

func TestServer_NonTemperCI_OKNoJIT(t *testing.T) {
	m := &mockMinter{}
	store := NewAssignmentStore()
	h := NewHandler(m, store, HandlerConfig{})
	srv := NewServer(ServerConfig{Handler: h, WebhookSecret: "super-secret"})

	body := fixture(t, "workflow_job_queued_ubuntu_latest.json")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(m.calls) != 0 {
		t.Fatalf("expected no JIT, got %d", len(m.calls))
	}
}

func TestServer_TemperCIQueued_MintsJIT(t *testing.T) {
	m := &mockMinter{}
	store := NewAssignmentStore()
	h := NewHandler(m, store, HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{Handler: h, WebhookSecret: "super-secret"})

	body := fixture(t, "workflow_job_queued_temperci.json")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(m.calls) != 1 {
		t.Fatalf("JIT calls = %d, want 1", len(m.calls))
	}
	if got := m.calls[0].Labels; len(got) != 1 || got[0] != "temperci-4vcpu-ubuntu-2404" {
		t.Errorf("labels = %v", got)
	}
	respBody, _ := io.ReadAll(rr.Body)
	if !bytes.Contains(respBody, []byte(`"minted":true`)) {
		t.Errorf("body = %s", respBody)
	}
}

func TestServer_Healthz(t *testing.T) {
	srv := NewServer(ServerConfig{
		Handler:       NewHandler(&mockMinter{}, NewAssignmentStore(), HandlerConfig{}),
		WebhookSecret: "x",
	})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.String() != "ok\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
}
