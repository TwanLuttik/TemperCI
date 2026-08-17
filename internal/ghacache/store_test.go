package ghacache

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestStore_PutGetExactKey(t *testing.T) {
	s := openTestStore(t, 1<<20)
	up, err := s.Create("acme/app", "node-modules", "v1")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("tarball-bytes")
	if err := s.WriteUpload(up.ID, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finalize("acme/app", "node-modules", "v1", int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("acme/app", "node-modules", nil, "v1")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Key != "node-modules" {
		t.Fatalf("key=%q", got.Key)
	}
	data, err := s.ReadBlob(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("blob=%q", data)
	}
}

func TestStore_RestoreKeyPrefixPicksNewest(t *testing.T) {
	s := openTestStore(t, 1<<20)
	writeEntry(t, s, "acme/app", "node-modules-aaa", "v1", []byte("old"))
	writeEntry(t, s, "acme/app", "node-modules-zzz", "v1", []byte("new"))

	got, ok, err := s.Get("acme/app", "missing", []string{"node-modules-"}, "v1")
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Key != "node-modules-zzz" {
		t.Fatalf("matched=%q want node-modules-zzz", got.Key)
	}
}

func TestStore_VersionMustMatch(t *testing.T) {
	s := openTestStore(t, 1<<20)
	writeEntry(t, s, "acme/app", "node-modules", "v1", []byte("x"))
	_, ok, err := s.Get("acme/app", "node-modules", nil, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected miss on version mismatch")
	}
}

func TestStore_RepoIsolation(t *testing.T) {
	s := openTestStore(t, 1<<20)
	writeEntry(t, s, "acme/app", "node-modules", "v1", []byte("a"))
	_, ok, err := s.Get("other/app", "node-modules", nil, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected miss across repos")
	}
	repos := s.Repos()
	if len(repos) != 1 || repos[0] != "acme/app" {
		t.Fatalf("repos=%v", repos)
	}
}

func TestStore_LRUEviction(t *testing.T) {
	s := openTestStore(t, 20)
	writeEntry(t, s, "acme/app", "old", "v1", []byte("1234567890")) // 10
	writeEntry(t, s, "acme/app", "new", "v1", []byte("1234567890")) // 10
	// Touch "new" so "old" is evicted when a third 10-byte entry arrives.
	if _, ok, err := s.Get("acme/app", "new", nil, "v1"); err != nil || !ok {
		t.Fatalf("touch new: ok=%v err=%v", ok, err)
	}
	writeEntry(t, s, "acme/app", "newer", "v1", []byte("1234567890"))

	if _, ok, _ := s.Get("acme/app", "old", nil, "v1"); ok {
		t.Fatal("expected old entry evicted")
	}
	if _, ok, _ := s.Get("acme/app", "new", nil, "v1"); !ok {
		t.Fatal("expected new entry kept")
	}
}

func writeEntry(t *testing.T, s *Store, repo, key, version string, payload []byte) {
	t.Helper()
	up, err := s.Create(repo, key, version)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteUpload(up.ID, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finalize(repo, key, version, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T, maxBytes int64) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "cache"), maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
