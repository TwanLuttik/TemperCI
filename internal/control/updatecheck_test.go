package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUpdateAvailable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.5", "v0.1.6", true},
		{"0.1.5", "v0.1.6", true},
		{"v0.1.5", "v0.1.5", false},
		{"v0.1.6", "v0.1.5", false},
		{"v0.1.5-3-gabc1234", "v0.1.5", false},
		{"v0.1.5-3-gabc1234", "v0.1.6", true},
		{"v0.1.5-dirty", "v0.1.5", false},
		{"dev", "v0.1.5", true},
		{"v0.1.5", "", false},
		{"", "v0.1.5", true},
		{"abc1234", "v0.1.5", true},
	}
	for _, tc := range cases {
		if got := updateAvailable(tc.current, tc.latest); got != tc.want {
			t.Errorf("updateAvailable(%q, %q)=%v want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestUpdateChecker_CachesLatestRelease(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/TwanLuttik/TemperCI/releases/latest" {
			t.Errorf("path=%s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.1.6",
			"html_url": "https://github.com/TwanLuttik/TemperCI/releases/tag/v0.1.6",
		})
	}))
	defer upstream.Close()

	c := NewUpdateChecker(UpdateCheckerConfig{
		Current: "v0.1.5",
		APIBase: upstream.URL,
		Client:  upstream.Client(),
		TTL:     time.Hour,
	})

	st := c.Status(context.Background())
	if !st.UpdateAvailable || st.Latest != "v0.1.6" || st.Version != "v0.1.5" {
		t.Fatalf("first status=%+v", st)
	}
	if st.ReleaseURL == "" {
		t.Fatal("expected release url")
	}

	st = c.Status(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", hits.Load())
	}
	if !st.UpdateAvailable {
		t.Fatalf("cached status=%+v", st)
	}
}

func TestUpdateChecker_Singleflight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.2.0",
			"html_url": "https://example.test/v0.2.0",
		})
	}))
	defer upstream.Close()

	c := NewUpdateChecker(UpdateCheckerConfig{
		Current: "v0.1.0",
		APIBase: upstream.URL,
		Client:  upstream.Client(),
		TTL:     time.Hour,
	})

	var wg sync.WaitGroup
	got := make([]VersionStatus, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = c.Status(context.Background())
		}(i)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not called")
	}
	close(release)
	wg.Wait()

	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}
	for i, st := range got {
		if st.Latest != "v0.2.0" || !st.UpdateAvailable {
			t.Fatalf("got[%d]=%+v", i, st)
		}
	}
}

func TestUpdateChecker_RefetchesAfterTTL(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		tag := "v0.1.6"
		if n > 1 {
			tag = "v0.1.7"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"html_url": "https://example.test/" + tag,
		})
	}))
	defer upstream.Close()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	c := NewUpdateChecker(UpdateCheckerConfig{
		Current: "v0.1.5",
		APIBase: upstream.URL,
		Client:  upstream.Client(),
		TTL:     time.Hour,
		Now:     func() time.Time { return now },
	})

	if st := c.Status(context.Background()); st.Latest != "v0.1.6" {
		t.Fatalf("first=%+v", st)
	}
	now = now.Add(time.Hour + time.Second)
	if st := c.Status(context.Background()); st.Latest != "v0.1.7" {
		t.Fatalf("after ttl=%+v", st)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits=%d want 2", hits.Load())
	}
}

func TestUpdateChecker_FailedCheckUsesShorterTTL(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer upstream.Close()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	c := NewUpdateChecker(UpdateCheckerConfig{
		Current: "v0.1.5",
		APIBase: upstream.URL,
		Client:  upstream.Client(),
		TTL:     time.Hour,
		FailTTL: 15 * time.Minute,
		Now:     func() time.Time { return now },
	})
	_ = c.Status(context.Background())
	now = now.Add(time.Minute)
	_ = c.Status(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1 inside fail TTL", hits.Load())
	}
	now = now.Add(15 * time.Minute)
	_ = c.Status(context.Background())
	if hits.Load() != 2 {
		t.Fatalf("hits=%d want 2 after fail TTL", hits.Load())
	}
}

func TestUpdateChecker_FailureDoesNotDropCurrentVersion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer upstream.Close()

	c := NewUpdateChecker(UpdateCheckerConfig{
		Current: "v0.1.5",
		APIBase: upstream.URL,
		Client:  upstream.Client(),
		TTL:     time.Hour,
	})
	st := c.Status(context.Background())
	if st.Version != "v0.1.5" {
		t.Fatalf("version=%q", st.Version)
	}
	if st.UpdateAvailable {
		t.Fatal("should not claim update on failed check")
	}
	if st.CheckError == "" {
		t.Fatal("expected check_error")
	}
}
