package agent

import (
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/ghacache"
)

func TestApplyCacheOps_PurgeRepoAndAll(t *testing.T) {
	st, err := ghacache.Open(filepath.Join(t.TempDir(), "c"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	writeCache(t, st, "acme/app", "k", []byte("111"))
	writeCache(t, st, "acme/other", "k", []byte("22"))

	n, err := ApplyCacheOps(st, []api.CacheOp{{Action: api.CacheOpPurgeRepo, Repo: "acme/app"}})
	if err != nil || n != 1 {
		t.Fatalf("purge repo n=%d err=%v", n, err)
	}
	if _, ok, _ := st.Get("acme/app", "k", nil, "v"); ok {
		t.Fatal("app should be gone")
	}
	if _, ok, _ := st.Get("acme/other", "k", nil, "v"); !ok {
		t.Fatal("other should remain")
	}

	n, err = ApplyCacheOps(st, []api.CacheOp{{Action: api.CacheOpPurgeAll}})
	if err != nil || n != 1 {
		t.Fatalf("purge all n=%d err=%v", n, err)
	}
	if st.Usage().Entries != 0 {
		t.Fatalf("usage=%+v", st.Usage())
	}
}

func writeCache(t *testing.T, st *ghacache.Store, repo, key string, payload []byte) {
	t.Helper()
	up, err := st.Create(repo, key, "v")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteUpload(up.ID, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Finalize(repo, key, "v", int64(len(payload))); err != nil {
		t.Fatal(err)
	}
}
