package ocicache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scope selects the public pool or a per-repo namespace.
type Scope string

const (
	ScopePublic Scope = "public"
	ScopeRepo   Scope = "repo"
)

// Manifest is a cached registry manifest (tag or digest).
type Manifest struct {
	Host      string `json:"host"`
	Name      string `json:"name"`
	Reference string `json:"reference"`
	MediaType string `json:"media_type"`
	Digest    string `json:"digest"`
	Body      []byte `json:"-"`
	Scope     Scope  `json:"scope"`
	Repo      string `json:"repo,omitempty"`
}

// RepoUsage is disk usage for one namespace ("" / "public" or "org/repo").
type RepoUsage struct {
	Repo       string    `json:"repo"`
	Bytes      int64     `json:"bytes"`
	Entries    int       `json:"entries"`
	LastAccess time.Time `json:"last_access,omitempty"`
}

// Usage is a point-in-time inventory.
type Usage struct {
	Bytes    int64       `json:"bytes"`
	MaxBytes int64       `json:"max_bytes"`
	Entries  int         `json:"entries"`
	Repos    []RepoUsage `json:"repos,omitempty"`
}

type blobMeta struct {
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	Scope      Scope     `json:"scope"`
	Repo       string    `json:"repo,omitempty"`
	Created    time.Time `json:"created"`
	LastAccess time.Time `json:"last_access"`
}

type manifestMeta struct {
	Host       string    `json:"host"`
	Name       string    `json:"name"`
	Reference  string    `json:"reference"`
	MediaType  string    `json:"media_type"`
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	Scope      Scope     `json:"scope"`
	Repo       string    `json:"repo,omitempty"`
	Created    time.Time `json:"created"`
	LastAccess time.Time `json:"last_access"`
}

// Store is a host-local OCI blob + manifest cache.
type Store struct {
	root     string
	maxBytes int64
	mu       sync.Mutex
}

// Open creates or loads a cache rooted at dir.
func Open(dir string, maxBytes int64) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("ocicache: empty dir")
	}
	if maxBytes <= 0 {
		maxBytes = 100 << 30
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("ocicache: mkdir: %w", err)
	}
	return &Store{root: dir, maxBytes: maxBytes}, nil
}

// PutBlob writes a content-addressed blob into scope.
func (s *Store) PutBlob(scope Scope, repo, digest string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateScopeRepo(scope, repo); err != nil {
		return err
	}
	hexPart, err := digestHex(digest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hexPart {
		return fmt.Errorf("ocicache: digest mismatch")
	}
	dir := s.blobDir(scope, repo, hexPart)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := os.WriteFile(filepath.Join(dir, "data"), data, 0o600); err != nil {
		return err
	}
	m := blobMeta{
		Digest:     digest,
		Size:       int64(len(data)),
		Scope:      scope,
		Repo:       repo,
		Created:    now,
		LastAccess: now,
	}
	if err := writeJSON(filepath.Join(dir, "meta.json"), m); err != nil {
		return err
	}
	return s.evictLocked(int64(len(data)))
}

// PutBlobFromReader streams a blob to disk and verifies digest. Does not
// buffer the whole layer in memory.
func (s *Store) PutBlobFromReader(scope Scope, repo, digest string, r io.Reader) error {
	if err := validateScopeRepo(scope, repo); err != nil {
		return err
	}
	hexPart, err := digestHex(digest)
	if err != nil {
		return err
	}
	h := sha256.New()
	tmp, err := os.CreateTemp(s.root, "blob-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, 2<<30))
	_ = tmp.Close()
	if err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != hexPart {
		_ = os.Remove(tmpName)
		return fmt.Errorf("ocicache: digest mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.blobDir(scope, repo, hexPart)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	dst := filepath.Join(dir, "data")
	if err := os.Rename(tmpName, dst); err != nil {
		// Cross-device: copy then remove.
		in, oerr := os.Open(tmpName)
		if oerr != nil {
			_ = os.Remove(tmpName)
			return oerr
		}
		out, oerr := os.Create(dst)
		if oerr != nil {
			_ = in.Close()
			_ = os.Remove(tmpName)
			return oerr
		}
		_, oerr = io.Copy(out, in)
		_ = in.Close()
		_ = out.Close()
		_ = os.Remove(tmpName)
		if oerr != nil {
			return oerr
		}
	}
	now := time.Now().UTC()
	m := blobMeta{
		Digest: digest, Size: n, Scope: scope, Repo: repo,
		Created: now, LastAccess: now,
	}
	if err := writeJSON(filepath.Join(dir, "meta.json"), m); err != nil {
		return err
	}
	return s.evictLocked(n)
}

// GetBlob reads a blob from the given scope.
func (s *Store) GetBlob(scope Scope, repo, digest string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hexPart, err := digestHex(digest)
	if err != nil {
		return nil, err
	}
	dir := s.blobDir(scope, repo, hexPart)
	data, err := os.ReadFile(filepath.Join(dir, "data"))
	if err != nil {
		return nil, err
	}
	s.touchBlobLocked(dir)
	return data, nil
}

// FindBlob looks up a digest in public, then the bound repo. repo may be empty.
func (s *Store) FindBlob(repo, digest string) (Scope, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hexPart, err := digestHex(digest)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(s.blobDir(ScopePublic, "", hexPart), "data")); err == nil {
		s.touchBlobLocked(s.blobDir(ScopePublic, "", hexPart))
		return ScopePublic, true
	}
	if repo != "" {
		if _, err := os.Stat(filepath.Join(s.blobDir(ScopeRepo, repo, hexPart), "data")); err == nil {
			s.touchBlobLocked(s.blobDir(ScopeRepo, repo, hexPart))
			return ScopeRepo, true
		}
	}
	return "", false
}

// OpenBlob opens the blob file for serving (caller closes).
func (s *Store) OpenBlob(scope Scope, repo, digest string) (*os.File, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hexPart, err := digestHex(digest)
	if err != nil {
		return nil, 0, err
	}
	dir := s.blobDir(scope, repo, hexPart)
	f, err := os.Open(filepath.Join(dir, "data"))
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	s.touchBlobLocked(dir)
	return f, st.Size(), nil
}

// PutManifest stores a manifest under its tag and digest keys.
func (s *Store) PutManifest(m Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateScopeRepo(m.Scope, m.Repo); err != nil {
		return err
	}
	if err := validateName(m.Name); err != nil {
		return err
	}
	if m.Host == "" || m.Reference == "" {
		return fmt.Errorf("ocicache: host and reference required")
	}
	if _, err := digestHex(m.Digest); err != nil {
		return err
	}
	now := time.Now().UTC()
	meta := manifestMeta{
		Host:       m.Host,
		Name:       m.Name,
		Reference:  m.Reference,
		MediaType:  m.MediaType,
		Digest:     m.Digest,
		Size:       int64(len(m.Body)),
		Scope:      m.Scope,
		Repo:       m.Repo,
		Created:    now,
		LastAccess: now,
	}
	refs := []string{m.Reference}
	if m.Reference != m.Digest {
		refs = append(refs, m.Digest)
	}
	for _, ref := range refs {
		dir := s.manifestDir(m.Scope, m.Repo, m.Host, m.Name, ref)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "data"), m.Body, 0o600); err != nil {
			return err
		}
		meta.Reference = ref
		if err := writeJSON(filepath.Join(dir, "meta.json"), meta); err != nil {
			return err
		}
	}
	return s.evictLocked(int64(len(m.Body)))
}

// FindManifest looks up public then repo. repo may be empty.
func (s *Store) FindManifest(repo, host, name, reference string) (Manifest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok, err := s.readManifestLocked(ScopePublic, "", host, name, reference); ok || err != nil {
		return m, ok, err
	}
	if repo != "" {
		return s.readManifestLocked(ScopeRepo, repo, host, name, reference)
	}
	return Manifest{}, false, nil
}

func (s *Store) readManifestLocked(scope Scope, repo, host, name, reference string) (Manifest, bool, error) {
	dir := s.manifestDir(scope, repo, host, name, reference)
	raw, err := os.ReadFile(filepath.Join(dir, "data"))
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, false, nil
		}
		return Manifest{}, false, err
	}
	var meta manifestMeta
	if err := readJSON(filepath.Join(dir, "meta.json"), &meta); err != nil {
		return Manifest{}, false, err
	}
	meta.LastAccess = time.Now().UTC()
	_ = writeJSON(filepath.Join(dir, "meta.json"), meta)
	return Manifest{
		Host:      meta.Host,
		Name:      meta.Name,
		Reference: meta.Reference,
		MediaType: meta.MediaType,
		Digest:    meta.Digest,
		Body:      raw,
		Scope:     meta.Scope,
		Repo:      meta.Repo,
	}, true, nil
}

// Repos lists org/repo namespaces that have at least one private object.
func (s *Store) Repos() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	_ = filepath.WalkDir(filepath.Join(s.root, "blobs", "repos"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		var m blobMeta
		if err := readJSON(path, &m); err == nil && m.Repo != "" {
			seen[m.Repo] = struct{}{}
		}
		return nil
	})
	_ = filepath.WalkDir(filepath.Join(s.root, "manifests", "repos"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		var m manifestMeta
		if err := readJSON(path, &m); err == nil && m.Repo != "" {
			seen[m.Repo] = struct{}{}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// Usage returns ready inventory including public as repo name "public".
func (s *Store) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageLocked()
}

func (s *Store) usageLocked() Usage {
	byRepo := map[string]*RepoUsage{}
	var total int64
	var entries int
	collect := func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		var size int64
		var repo string
		var last time.Time
		var bm blobMeta
		if err := readJSON(path, &bm); err == nil && bm.Digest != "" && bm.Size > 0 {
			size = bm.Size
			last = bm.LastAccess
			if bm.Scope == ScopeRepo {
				repo = bm.Repo
			} else {
				repo = "public"
			}
		} else {
			var mm manifestMeta
			if err := readJSON(path, &mm); err != nil || mm.Digest == "" {
				return nil
			}
			size = mm.Size
			last = mm.LastAccess
			if mm.Scope == ScopeRepo {
				repo = mm.Repo
			} else {
				repo = "public"
			}
		}
		if repo == "" {
			return nil
		}
		entries++
		total += size
		ru := byRepo[repo]
		if ru == nil {
			ru = &RepoUsage{Repo: repo}
			byRepo[repo] = ru
		}
		ru.Bytes += size
		ru.Entries++
		if last.After(ru.LastAccess) {
			ru.LastAccess = last
		}
		return nil
	}
	_ = filepath.WalkDir(s.root, collect)
	out := Usage{Bytes: total, MaxBytes: s.maxBytes, Entries: entries}
	for _, ru := range byRepo {
		out.Repos = append(out.Repos, *ru)
	}
	sort.Slice(out.Repos, func(i, j int) bool { return out.Repos[i].Repo < out.Repos[j].Repo })
	return out
}

// DeleteRepo removes private objects for org/repo. Public objects stay.
func (s *Store) DeleteRepo(repo string) (entries int, bytes int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateRepo(repo); err != nil {
		return 0, 0, err
	}
	u := s.usageLocked()
	for _, ru := range u.Repos {
		if ru.Repo == repo {
			entries, bytes = ru.Entries, ru.Bytes
			break
		}
	}
	if err := os.RemoveAll(filepath.Join(s.root, "blobs", "repos", repo)); err != nil {
		return entries, bytes, err
	}
	if err := os.RemoveAll(filepath.Join(s.root, "manifests", "repos", repo)); err != nil {
		return entries, bytes, err
	}
	return entries, bytes, nil
}

// DeleteAll removes public and private objects.
func (s *Store) DeleteAll() (entries int, bytes int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usageLocked()
	entries, bytes = u.Entries, u.Bytes
	for _, name := range []string{"blobs", "manifests", "uploads"} {
		if err := os.RemoveAll(filepath.Join(s.root, name)); err != nil {
			return entries, bytes, err
		}
	}
	return entries, bytes, nil
}

func (s *Store) evictLocked(incoming int64) error {
	for {
		u := s.usageLocked()
		if u.Bytes <= s.maxBytes {
			return nil
		}
		type item struct {
			path string
			size int64
			last time.Time
		}
		var items []item
		_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "meta.json" {
				return nil
			}
			dir := filepath.Dir(path)
			var last time.Time
			var size int64
			var bm blobMeta
			if err := readJSON(path, &bm); err == nil && bm.Digest != "" {
				last, size = bm.LastAccess, bm.Size
			} else {
				var mm manifestMeta
				if err := readJSON(path, &mm); err != nil {
					return nil
				}
				last, size = mm.LastAccess, mm.Size
			}
			items = append(items, item{path: dir, size: size, last: last})
			return nil
		})
		if len(items) == 0 {
			return fmt.Errorf("ocicache: cannot evict")
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].last.Equal(items[j].last) {
				return items[i].size > items[j].size
			}
			return items[i].last.Before(items[j].last)
		})
		// Do not delete the object we just wrote if it is the only one that
		// would bring us under cap — evict the oldest *other* item when possible.
		victim := items[0]
		if err := os.RemoveAll(victim.path); err != nil {
			return err
		}
		_ = incoming
	}
}

func (s *Store) touchBlobLocked(dir string) {
	var m blobMeta
	p := filepath.Join(dir, "meta.json")
	if err := readJSON(p, &m); err != nil {
		return
	}
	m.LastAccess = time.Now().UTC()
	_ = writeJSON(p, m)
}

func (s *Store) blobDir(scope Scope, repo, hexPart string) string {
	if scope == ScopeRepo {
		return filepath.Join(s.root, "blobs", "repos", repo, "sha256", hexPart)
	}
	return filepath.Join(s.root, "blobs", "public", "sha256", hexPart)
}

func (s *Store) manifestDir(scope Scope, repo, host, name, ref string) string {
	safeRef := hex.EncodeToString([]byte(ref))
	if scope == ScopeRepo {
		return filepath.Join(s.root, "manifests", "repos", repo, host, name, safeRef)
	}
	return filepath.Join(s.root, "manifests", "public", host, name, safeRef)
}

func validateScopeRepo(scope Scope, repo string) error {
	switch scope {
	case ScopePublic:
		return nil
	case ScopeRepo:
		return validateRepo(repo)
	default:
		return fmt.Errorf("ocicache: invalid scope %q", scope)
	}
}

func validateRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("ocicache: repo must be org/name")
	}
	for _, p := range parts {
		if p == "." || p == ".." || strings.ContainsAny(p, `\:`) {
			return fmt.Errorf("ocicache: invalid repo %q", repo)
		}
	}
	return nil
}

func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return fmt.Errorf("ocicache: invalid name %q", name)
	}
	return nil
}

func digestHex(digest string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(digest))
	const prefix = "sha256:"
	if !strings.HasPrefix(d, prefix) {
		return "", fmt.Errorf("ocicache: digest must be sha256")
	}
	h := d[len(prefix):]
	if len(h) != 64 {
		return "", fmt.Errorf("ocicache: digest must be sha256:<64 hex>")
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("ocicache: digest must be sha256:<64 hex>")
		}
	}
	return h, nil
}

func writeJSON(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
