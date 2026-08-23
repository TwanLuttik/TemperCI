package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestVersionEndpoint_ReportsUpdate(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.1.6",
			"html_url": "https://github.com/TwanLuttik/TemperCI/releases/tag/v0.1.6",
		})
	}))
	defer upstream.Close()

	srv := NewServer(ServerConfig{
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: true,
			},
			Version: "v0.1.5",
			Updates: NewUpdateChecker(UpdateCheckerConfig{
				Current: "v0.1.5",
				APIBase: upstream.URL,
				Client:  upstream.Client(),
			}),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body VersionStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "v0.1.5" || body.Latest != "v0.1.6" || !body.UpdateAvailable {
		t.Fatalf("body=%+v", body)
	}

	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}
}

func TestVersionEndpoint_WorksWithoutChecker(t *testing.T) {
	srv := NewServer(ServerConfig{
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: true,
			},
			Version: "v0.1.5",
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body VersionStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "v0.1.5" {
		t.Fatalf("body=%+v", body)
	}
}
