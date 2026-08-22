package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/ghacache"
	"github.com/TwanLuttik/TemperCI/internal/ocicache"
)

func TestApplyCacheOps_PurgeRepoAndAll(t *testing.T) {
	st, err := ghacache.Open(filepath.Join(t.TempDir(), "c"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	oci, err := ocicache.Open(filepath.Join(t.TempDir(), "o"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	writeCache(t, st, "acme/app", "k", []byte("111"))
	writeCache(t, st, "acme/other", "k", []byte("22"))
	writeOCI(t, oci, "acme/app", []byte("priv"))
	pub := []byte("pub")
	pubSum := sha256.Sum256(pub)
	if err := oci.PutBlob(ocicache.ScopePublic, "", "sha256:"+hex.EncodeToString(pubSum[:]), pub); err != nil {
		t.Fatal(err)
	}

	n, err := ApplyCacheOps(st, oci, []api.CacheOp{{Action: api.CacheOpPurgeRepo, Repo: "acme/app"}})
	if err != nil || n != 1 {
		t.Fatalf("purge repo n=%d err=%v", n, err)
	}
	if _, ok, _ := st.Get("acme/app", "k", nil, "v"); ok {
		t.Fatal("app should be gone")
	}
	if _, ok, _ := st.Get("acme/other", "k", nil, "v"); !ok {
		t.Fatal("other should remain")
	}
	sum := sha256.Sum256([]byte("priv"))
	if _, ok := oci.FindBlob("acme/app", "sha256:"+hex.EncodeToString(sum[:])); ok {
		t.Fatal("oci repo blob should be gone")
	}
	if _, ok := oci.FindBlob("", "sha256:"+hex.EncodeToString(pubSum[:])); !ok {
		t.Fatal("public oci blob must survive repo purge")
	}

	n, err = ApplyCacheOps(st, oci, []api.CacheOp{{Action: api.CacheOpPurgeAll}})
	if err != nil || n != 1 {
		t.Fatalf("purge all n=%d err=%v", n, err)
	}
	if st.Usage().Entries != 0 {
		t.Fatalf("usage=%+v", st.Usage())
	}
	if oci.Usage().Entries != 0 {
		t.Fatalf("oci usage=%+v", oci.Usage())
	}
}

func TestCacheUsageFromStores_PrefixesOCI(t *testing.T) {
	st, err := ghacache.Open(filepath.Join(t.TempDir(), "c"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	oci, err := ocicache.Open(filepath.Join(t.TempDir(), "o"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	writeCache(t, st, "acme/app", "k", []byte("111"))
	writeOCI(t, oci, "acme/app", []byte("priv"))
	u := CacheUsageFromStores(st, oci)
	if u == nil || u.Entries < 2 {
		t.Fatalf("usage=%+v", u)
	}
	var sawActions, sawOCI bool
	for _, r := range u.Repos {
		if r.Repo == "acme/app" {
			sawActions = true
		}
		if r.Repo == "oci:acme/app" {
			sawOCI = true
		}
	}
	if !sawActions || !sawOCI {
		t.Fatalf("repos=%+v", u.Repos)
	}
}

func writeOCI(t *testing.T, st *ocicache.Store, repo string, payload []byte) {
	t.Helper()
	sum := sha256.Sum256(payload)
	if err := st.PutBlob(ocicache.ScopeRepo, repo, "sha256:"+hex.EncodeToString(sum[:]), payload); err != nil {
		t.Fatal(err)
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
