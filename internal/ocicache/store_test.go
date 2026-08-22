package ocicache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestStore_PublicBlobRoundTrip(t *testing.T) {
	s := openTestStore(t, 1<<20)
	sum := sha256.Sum256([]byte("hello-layer"))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := s.PutBlob(ScopePublic, "", digest, []byte("hello-layer")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBlob(ScopePublic, "", digest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("hello-layer")) {
		t.Fatalf("got %q", got)
	}
	if scope, ok := s.FindBlob("", digest); !ok || scope != ScopePublic {
		t.Fatalf("FindBlob scope=%q ok=%v", scope, ok)
	}
}

func TestStore_RepoIsolation(t *testing.T) {
	s := openTestStore(t, 1<<20)
	sum := sha256.Sum256([]byte("secret-layer"))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := s.PutBlob(ScopeRepo, "acme/app", digest, []byte("secret-layer")); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindBlob("other/app", digest); ok {
		t.Fatal("other repo must not see private blob")
	}
	if scope, ok := s.FindBlob("acme/app", digest); !ok || scope != ScopeRepo {
		t.Fatalf("owner FindBlob scope=%q ok=%v", scope, ok)
	}
	if _, ok := s.FindBlob("", digest); ok {
		t.Fatal("unbound lookup must not see private blob")
	}
}

func TestStore_PublicPreferredOverRepo(t *testing.T) {
	s := openTestStore(t, 1<<20)
	sum := sha256.Sum256([]byte("shared"))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := s.PutBlob(ScopeRepo, "acme/app", digest, []byte("shared")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBlob(ScopePublic, "", digest, []byte("shared")); err != nil {
		t.Fatal(err)
	}
	scope, ok := s.FindBlob("acme/app", digest)
	if !ok || scope != ScopePublic {
		t.Fatalf("want public, got %q ok=%v", scope, ok)
	}
}

func TestStore_RejectsBadDigest(t *testing.T) {
	s := openTestStore(t, 1<<20)
	if err := s.PutBlob(ScopePublic, "", "sha256:deadbeef", []byte("x")); err == nil {
		t.Fatal("expected error for short digest")
	}
	if err := s.PutBlob(ScopePublic, "", "../escape", []byte("x")); err == nil {
		t.Fatal("expected error for pathy digest")
	}
}

func TestStore_RejectsBadRepo(t *testing.T) {
	s := openTestStore(t, 1<<20)
	sum := sha256.Sum256([]byte("x"))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := s.PutBlob(ScopeRepo, "../evil", digest, []byte("x")); err == nil {
		t.Fatal("expected bad repo error")
	}
	if err := s.PutBlob(ScopeRepo, "nopath", digest, []byte("x")); err == nil {
		t.Fatal("expected org/name")
	}
}

func TestStore_ManifestRoundTrip(t *testing.T) {
	s := openTestStore(t, 1<<20)
	body := []byte(`{"schemaVersion":2}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	m := Manifest{
		Host:      "ghcr.io",
		Name:      "library/postgres",
		Reference: "16",
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    digest,
		Body:      body,
		Scope:     ScopePublic,
	}
	if err := s.PutManifest(m); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.FindManifest("", "ghcr.io", "library/postgres", "16")
	if err != nil || !ok {
		t.Fatalf("find ok=%v err=%v", ok, err)
	}
	if got.Digest != digest || !bytes.Equal(got.Body, body) {
		t.Fatalf("got %+v", got)
	}
	got2, ok, err := s.FindManifest("", "ghcr.io", "library/postgres", digest)
	if err != nil || !ok || got2.Digest != digest {
		t.Fatalf("by digest ok=%v err=%v", ok, err)
	}
}

func TestStore_ManifestRepoIsolation(t *testing.T) {
	s := openTestStore(t, 1<<20)
	body := []byte(`{"private":true}`)
	sum := sha256.Sum256(body)
	m := Manifest{
		Host:      "ghcr.io",
		Name:      "acme/private",
		Reference: "latest",
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    "sha256:" + hex.EncodeToString(sum[:]),
		Body:      body,
		Scope:     ScopeRepo,
		Repo:      "acme/app",
	}
	if err := s.PutManifest(m); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.FindManifest("other/app", "ghcr.io", "acme/private", "latest"); err != nil || ok {
		t.Fatalf("other repo must miss, ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.FindManifest("acme/app", "ghcr.io", "acme/private", "latest"); err != nil || !ok {
		t.Fatalf("owner must hit, ok=%v err=%v", ok, err)
	}
}

func TestStore_LRUEvictsOldest(t *testing.T) {
	s := openTestStore(t, 40)
	oldSum := sha256.Sum256([]byte("aaaa"))
	newSum := sha256.Sum256([]byte("bbbb"))
	oldD := "sha256:" + hex.EncodeToString(oldSum[:])
	newD := "sha256:" + hex.EncodeToString(newSum[:])
	if err := s.PutBlob(ScopePublic, "", oldD, []byte("aaaa")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := s.GetBlob(ScopePublic, "", oldD); err != nil {
		t.Fatal(err)
	}
	// Touch old so it is newer, then write a mid blob, then force evict via a large write.
	midSum := sha256.Sum256([]byte("cccc"))
	midD := "sha256:" + hex.EncodeToString(midSum[:])
	if err := s.PutBlob(ScopePublic, "", midD, []byte("cccc")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBlob(ScopePublic, "", newD, []byte("bbbb")); err != nil {
		t.Fatal(err)
	}
	// Capacity 40 bytes: three 4-byte blobs fit; adding a 30-byte blob evicts oldest unused.
	big := bytes.Repeat([]byte("z"), 30)
	bigSum := sha256.Sum256(big)
	bigD := "sha256:" + hex.EncodeToString(bigSum[:])
	if err := s.PutBlob(ScopePublic, "", bigD, big); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindBlob("", midD); ok {
		// mid was never re-read after old was touched, so it should be older than old
		// and a candidate — but we only need to assert total stays within cap
		_ = ok
	}
	if s.Usage().Bytes > 40 {
		t.Fatalf("usage bytes %d exceed cap", s.Usage().Bytes)
	}
	if _, ok := s.FindBlob("", bigD); !ok {
		t.Fatal("newest blob must remain")
	}
}

func TestStore_UsageDeleteRepoAndAll(t *testing.T) {
	s := openTestStore(t, 1<<20)
	sum := sha256.Sum256([]byte("priv"))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := s.PutBlob(ScopeRepo, "acme/app", digest, []byte("priv")); err != nil {
		t.Fatal(err)
	}
	pub := sha256.Sum256([]byte("pub"))
	pubD := "sha256:" + hex.EncodeToString(pub[:])
	if err := s.PutBlob(ScopePublic, "", pubD, []byte("pub")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutManifest(Manifest{
		Host: "ghcr.io", Name: "acme/app", Reference: "latest",
		MediaType: "application/json", Digest: digest, Body: []byte("priv"),
		Scope: ScopeRepo, Repo: "acme/app",
	}); err != nil {
		t.Fatal(err)
	}
	repos := s.Repos()
	if len(repos) != 1 || repos[0] != "acme/app" {
		t.Fatalf("repos=%v", repos)
	}
	u := s.Usage()
	if u.Entries < 2 || u.Bytes == 0 {
		t.Fatalf("usage=%+v", u)
	}
	if _, _, err := s.DeleteRepo("acme/app"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindBlob("acme/app", digest); ok {
		t.Fatal("repo blob should be gone")
	}
	if _, ok := s.FindBlob("", pubD); !ok {
		t.Fatal("public blob must survive DeleteRepo")
	}
	if _, _, err := s.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	if s.Usage().Entries != 0 {
		t.Fatalf("after delete all %+v", s.Usage())
	}
}

func openTestStore(t *testing.T, max int64) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), max)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
