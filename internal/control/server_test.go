package control

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/store"
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
	respBody, _ := io.ReadAll(rr.Body)
	if !bytes.Contains(respBody, []byte(`"accepted":true`)) {
		t.Errorf("body = %s", respBody)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && store.Get(991001) == nil {
		time.Sleep(5 * time.Millisecond)
	}
	if store.Get(991001) == nil {
		t.Fatal("assignment never minted")
	}
	if len(m.calls) != 1 {
		t.Fatalf("JIT calls = %d, want 1", len(m.calls))
	}
	if got := m.calls[0].Labels; len(got) != 1 || got[0] != "temperci-4vcpu-ubuntu-2404" {
		t.Errorf("labels = %v", got)
	}
}

func TestServer_PingRecordsWebhookDelivery(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(ServerConfig{
		WebhookSecret: "super-secret",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: false,
				ListenAddr:     "127.0.0.1:8080",
			},
			Store: st,
		},
	})
	body := []byte(`{"zen":"ok","hook_id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "deliv-1")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	got, err := st.LastWebhookDelivery()
	if err != nil || got == nil {
		t.Fatalf("delivery=%v err=%v", got, err)
	}
	if got.Event != "ping" || got.Delivery != "deliv-1" {
		t.Fatalf("delivery=%+v", got)
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
