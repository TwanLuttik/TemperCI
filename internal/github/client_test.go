package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testRSAPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

func TestClient_GenerateJITConfig(t *testing.T) {
	var mu sync.Mutex
	var sawAuth string
	var sawPath string
	var sawBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/access_tokens"):
			// installation token exchange
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				http.Error(w, "missing jwt", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_install_token",
				"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/generate-jitconfig"):
			sawAuth = r.Header.Get("Authorization")
			sawPath = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &sawBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runner": map[string]any{
					"id":   42,
					"name": "temperci-job-991001",
				},
				"encoded_jit_config": "base64-jit-config-payload",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pemBytes := testRSAPrivateKeyPEM(t)
	c, err := NewClient(Config{
		AppID:          99,
		PrivateKeyPEM:  pemBytes,
		BaseURL:        srv.URL,
		HTTPClient:     srv.Client(),
		InstallationID: 12345,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := c.GenerateJITConfig(context.Background(), GenerateJITConfigRequest{
		Org:           "acme",
		Name:          "temperci-job-991001",
		RunnerGroupID: 1,
		Labels:        []string{"temperci-4vcpu-ubuntu-2404"},
	})
	if err != nil {
		t.Fatalf("GenerateJITConfig: %v", err)
	}
	if resp.EncodedJITConfig != "base64-jit-config-payload" {
		t.Errorf("encoded = %q", resp.EncodedJITConfig)
	}
	if resp.Runner.ID != 42 {
		t.Errorf("runner.id = %d", resp.Runner.ID)
	}

	mu.Lock()
	defer mu.Unlock()
	if sawAuth != "Bearer ghs_install_token" {
		t.Errorf("auth = %q, want installation token", sawAuth)
	}
	if !strings.HasSuffix(sawPath, "/orgs/acme/actions/runners/generate-jitconfig") {
		t.Errorf("path = %q", sawPath)
	}
	labels, _ := sawBody["labels"].([]any)
	if len(labels) != 1 || labels[0] != "temperci-4vcpu-ubuntu-2404" {
		t.Errorf("request labels = %v", sawBody["labels"])
	}
	if sawBody["runner_group_id"] != float64(1) {
		t.Errorf("runner_group_id = %v", sawBody["runner_group_id"])
	}
}

func TestClient_GenerateJITConfig_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_install_token",
				"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
			})
			return
		}
		http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		AppID:          1,
		PrivateKeyPEM:  testRSAPrivateKeyPEM(t),
		BaseURL:        srv.URL,
		HTTPClient:     srv.Client(),
		InstallationID: 9,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.GenerateJITConfig(context.Background(), GenerateJITConfigRequest{
		Org:           "acme",
		Name:          "r1",
		RunnerGroupID: 1,
		Labels:        []string{"temperci-2vcpu-ubuntu-2404"},
	})
	if err == nil {
		t.Fatal("expected API error")
	}
}
