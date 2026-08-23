// Package ghacache is a host-local GitHub Actions cache v2 store and gateway.
package ghacache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrExists is returned when Create is called for a finalized key+version.
var ErrExists = errors.New("ghacache: entry exists")

// Store is a repo-scoped blob cache on local disk.
type Store struct {
	root     string
	maxBytes int64
	mu       sync.Mutex
}

// Upload is an in-flight CreateCacheEntry.
type Upload struct {
	ID      string
	Repo    string
	Key     string
	Version string
}

// Entry is a finalized cache object.
type Entry struct {
	ID         string
	Repo       string
	Key        string
	Version    string
	Size       int64
	Created    time.Time
	LastAccess time.Time
}

// EntryUsage is one finalized cache key inside a repo namespace.
type EntryUsage struct {
	Key        string    `json:"key"`
	Version    string    `json:"version,omitempty"`
	Bytes      int64     `json:"bytes"`
	Created    time.Time `json:"created,omitempty"`
	LastAccess time.Time `json:"last_access,omitempty"`
}

// RepoUsage is disk usage for one org/repo namespace.
type RepoUsage struct {
	Repo       string       `json:"repo"`
	Bytes      int64        `json:"bytes"`
	Entries    int          `json:"entries"`
	LastAccess time.Time    `json:"last_access,omitempty"`
	Keys       []EntryUsage `json:"keys,omitempty"`
}

// Usage is a point-in-time inventory of the host cache.
type Usage struct {
	Bytes    int64       `json:"bytes"`
	MaxBytes int64       `json:"max_bytes"`
	Entries  int         `json:"entries"`
	Repos    []RepoUsage `json:"repos,omitempty"`
}

type meta struct {
	ID         string    `json:"id"`
	Repo       string    `json:"repo"`
	Key        string    `json:"key"`
	Version    string    `json:"version"`
	Size       int64     `json:"size"`
	State      string    `json:"state"` // upload | ready
	Created    time.Time `json:"created"`
	LastAccess time.Time `json:"last_access"`
}

// Open creates or loads a cache rooted at dir.
func Open(dir string, maxBytes int64) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("ghacache: empty dir")
	}
	if maxBytes <= 0 {
		maxBytes = 50 << 30
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("ghacache: mkdir: %w", err)
	}
	return &Store{root: dir, maxBytes: maxBytes}, nil
}

// Close is a no-op for the filesystem store (satisfies callers that defer Close).
func (s *Store) Close() error { return nil }

// Repos lists org/repo namespaces that have at least one finalized entry.
func (s *Store) Repos() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() != "meta.json" {
			return nil
		}
		m, err := readMeta(path)
		if err != nil || m.State != "ready" || m.Repo == "" {
			return nil
		}
		seen[m.Repo] = struct{}{}
		return nil
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// Usage returns finalized cache inventory (ready entries only).
func (s *Store) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageLocked()
}

func (s *Store) usageLocked() Usage {
	byRepo := map[string]*RepoUsage{}
	var total int64
	var entries int
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		m, err := readMeta(path)
		if err != nil || m.State != "ready" || m.Repo == "" {
			return nil
		}
		entries++
		total += m.Size
		ru := byRepo[m.Repo]
		if ru == nil {
			ru = &RepoUsage{Repo: m.Repo}
			byRepo[m.Repo] = ru
		}
		ru.Bytes += m.Size
		ru.Entries++
		if m.LastAccess.After(ru.LastAccess) {
			ru.LastAccess = m.LastAccess
		}
		ru.Keys = append(ru.Keys, EntryUsage{
			Key:        m.Key,
			Version:    m.Version,
			Bytes:      m.Size,
			Created:    m.Created,
			LastAccess: m.LastAccess,
		})
		return nil
	})
	out := Usage{Bytes: total, MaxBytes: s.maxBytes, Entries: entries}
	for _, ru := range byRepo {
		sort.Slice(ru.Keys, func(i, j int) bool {
			if ru.Keys[i].Bytes != ru.Keys[j].Bytes {
				return ru.Keys[i].Bytes > ru.Keys[j].Bytes
			}
			return ru.Keys[i].Key < ru.Keys[j].Key
		})
		out.Repos = append(out.Repos, *ru)
	}
	sort.Slice(out.Repos, func(i, j int) bool { return out.Repos[i].Repo < out.Repos[j].Repo })
	return out
}

// DeleteRepo removes finalized entries for one org/repo. In-flight uploads are left alone.
func (s *Store) DeleteRepo(repo string) (entries int, bytes int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateRepo(repo); err != nil {
		return 0, 0, err
	}
	dir := filepath.Join(s.repoDir(repo), "entries")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(dir, e.Name(), "meta.json")
		m, rerr := readMeta(metaPath)
		if rerr == nil && m.State == "ready" {
			entries++
			bytes += m.Size
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return entries, bytes, err
		}
	}
	return entries, bytes, nil
}

// DeleteAll removes every finalized entry and leftover upload. The CA dir is kept.
func (s *Store) DeleteAll() (entries int, bytes int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usageLocked()
	entries, bytes = u.Entries, u.Bytes
	rootEnts, err := os.ReadDir(s.root)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range rootEnts {
		name := e.Name()
		if name == "ca" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.root, name)); err != nil {
			return entries, bytes, err
		}
	}
	return entries, bytes, nil
}

// Create starts an upload for key+version. Returns ErrExists if already finalized.
func (s *Store) Create(repo, key, version string) (*Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateRepo(repo); err != nil {
		return nil, err
	}
	if key == "" || version == "" {
		return nil, fmt.Errorf("ghacache: key and version required")
	}
	if _, err := s.lookupLocked(repo, key, nil, version); err == nil {
		return nil, ErrExists
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	m := meta{
		ID:         id,
		Repo:       repo,
		Key:        key,
		Version:    version,
		State:      "upload",
		Created:    now,
		LastAccess: now,
	}
	dir := s.uploadDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeMeta(filepath.Join(dir, "meta.json"), m); err != nil {
		return nil, err
	}
	return &Upload{ID: id, Repo: repo, Key: key, Version: version}, nil
}

// WriteUpload replaces the in-flight blob body.
func (s *Store) WriteUpload(uploadID string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.uploadDir(uploadID)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return fmt.Errorf("ghacache: unknown upload %s", uploadID)
	}
	return os.WriteFile(filepath.Join(dir, "data"), data, 0o600)
}

// AppendUpload writes a named block (Azure Put Block).
func (s *Store) AppendUpload(uploadID, blockID string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.uploadDir(uploadID)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return fmt.Errorf("ghacache: unknown upload %s", uploadID)
	}
	if err := os.MkdirAll(filepath.Join(dir, "blocks"), 0o755); err != nil {
		return err
	}
	safe := hex.EncodeToString([]byte(blockID))
	return os.WriteFile(filepath.Join(dir, "blocks", safe), data, 0o600)
}

// CommitBlocks concatenates blocks in order into the upload payload.
func (s *Store) CommitBlocks(uploadID string, blockIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.uploadDir(uploadID)
	out, err := os.Create(filepath.Join(dir, "data"))
	if err != nil {
		return err
	}
	defer out.Close()
	for _, id := range blockIDs {
		safe := hex.EncodeToString([]byte(id))
		b, err := os.ReadFile(filepath.Join(dir, "blocks", safe))
		if err != nil {
			return fmt.Errorf("ghacache: missing block %q: %w", id, err)
		}
		if _, err := out.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// Finalize promotes an upload to a ready entry and evicts LRU if needed.
func (s *Store) Finalize(repo, key, version string, size int64) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, err := s.findUploadLocked(repo, key, version)
	if err != nil {
		return nil, err
	}
	src := filepath.Join(s.uploadDir(up.ID), "data")
	st, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("ghacache: upload has no data: %w", err)
	}
	if size <= 0 {
		size = st.Size()
	}
	eid := entryID(repo, key, version)
	dir := s.entryDir(repo, eid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, "data")
	if err := os.Rename(src, dst); err != nil {
		// Cross-device: copy.
		if err := copyFile(src, dst); err != nil {
			return nil, err
		}
		_ = os.Remove(src)
	}
	now := time.Now().UTC()
	m := meta{
		ID:         eid,
		Repo:       repo,
		Key:        key,
		Version:    version,
		Size:       size,
		State:      "ready",
		Created:    now,
		LastAccess: now,
	}
	if err := writeMeta(filepath.Join(dir, "meta.json"), m); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(s.uploadDir(up.ID))
	if err := s.evictLocked(eid); err != nil {
		return nil, err
	}
	return &Entry{ID: eid, Repo: repo, Key: key, Version: version, Size: size, Created: now, LastAccess: now}, nil
}

// Get resolves exact key then restore-key prefixes. Version must match.
func (s *Store) Get(repo, key string, restoreKeys []string, version string) (*Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.lookupLocked(repo, key, restoreKeys, version)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	e.LastAccess = time.Now().UTC()
	_ = writeMeta(filepath.Join(s.entryDir(repo, e.ID), "meta.json"), meta{
		ID: e.ID, Repo: e.Repo, Key: e.Key, Version: e.Version,
		Size: e.Size, State: "ready", Created: e.Created, LastAccess: e.LastAccess,
	})
	return e, true, nil
}

// ReadBlob returns the finalized payload.
func (s *Store) ReadBlob(entryID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.blobPathLocked(entryID)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// OpenBlob opens the finalized payload for ranged reads. Caller must Close.
func (s *Store) OpenBlob(entryID string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.blobPathLocked(entryID)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// ReadUpload returns the in-flight payload (used by the blob PUT handler).
func (s *Store) ReadUpload(uploadID string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.uploadDir(uploadID), "data"))
}

func (s *Store) blobPathLocked(entryID string) (string, error) {
	var found string
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		m, err := readMeta(path)
		if err != nil || m.ID != entryID || m.State != "ready" {
			return nil
		}
		found = filepath.Join(filepath.Dir(path), "data")
		return io.EOF
	})
	if found == "" {
		return "", fmt.Errorf("ghacache: blob %s not found", entryID)
	}
	return found, nil
}

func (s *Store) lookupLocked(repo, key string, restoreKeys []string, version string) (*Entry, error) {
	entries, err := s.listReadyLocked(repo)
	if err != nil {
		return nil, err
	}
	if key != "" {
		for i := range entries {
			if entries[i].Key == key && entries[i].Version == version {
				e := entries[i]
				return &e, nil
			}
		}
	}
	for _, prefix := range restoreKeys {
		if prefix == "" {
			continue
		}
		var best *Entry
		for i := range entries {
			e := entries[i]
			if e.Version != version || !strings.HasPrefix(e.Key, prefix) {
				continue
			}
			if best == nil || e.Created.After(best.Created) || (e.Created.Equal(best.Created) && e.Key > best.Key) {
				cp := e
				best = &cp
			}
		}
		if best != nil {
			return best, nil
		}
	}
	return nil, os.ErrNotExist
}

func (s *Store) listReadyLocked(repo string) ([]Entry, error) {
	dir := s.repoDir(repo)
	ents, err := os.ReadDir(filepath.Join(dir, "entries"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		m, err := readMeta(filepath.Join(dir, "entries", e.Name(), "meta.json"))
		if err != nil || m.State != "ready" {
			continue
		}
		out = append(out, Entry{
			ID: m.ID, Repo: m.Repo, Key: m.Key, Version: m.Version,
			Size: m.Size, Created: m.Created, LastAccess: m.LastAccess,
		})
	}
	return out, nil
}

func (s *Store) findUploadLocked(repo, key, version string) (*Upload, error) {
	uploads := filepath.Join(s.root, "uploads")
	ents, err := os.ReadDir(uploads)
	if err != nil {
		return nil, fmt.Errorf("ghacache: no upload for %s %s", key, version)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		m, err := readMeta(filepath.Join(uploads, e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		if m.Repo == repo && m.Key == key && m.Version == version && m.State == "upload" {
			return &Upload{ID: m.ID, Repo: m.Repo, Key: m.Key, Version: m.Version}, nil
		}
	}
	return nil, fmt.Errorf("ghacache: no upload for %s %s", key, version)
}

func (s *Store) evictLocked(keepID string) error {
	type item struct {
		path       string
		size       int64
		lastAccess time.Time
		id         string
	}
	var items []item
	var total int64
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		m, err := readMeta(path)
		if err != nil || m.State != "ready" {
			return nil
		}
		total += m.Size
		items = append(items, item{
			path:       filepath.Dir(path),
			size:       m.Size,
			lastAccess: m.LastAccess,
			id:         m.ID,
		})
		return nil
	})
	if total <= s.maxBytes {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].lastAccess.Equal(items[j].lastAccess) {
			return items[i].id < items[j].id
		}
		return items[i].lastAccess.Before(items[j].lastAccess)
	})
	for _, it := range items {
		if total <= s.maxBytes {
			break
		}
		if it.id == keepID {
			continue
		}
		if err := os.RemoveAll(it.path); err != nil {
			return err
		}
		total -= it.size
	}
	return nil
}

func (s *Store) repoDir(repo string) string {
	return filepath.Join(s.root, filepath.FromSlash(repo))
}

func (s *Store) entryDir(repo, id string) string {
	return filepath.Join(s.repoDir(repo), "entries", id)
}

func (s *Store) uploadDir(id string) string {
	return filepath.Join(s.root, "uploads", id)
}

func entryID(repo, key, version string) string {
	sum := sha256.Sum256([]byte(repo + "\x00" + key + "\x00" + version))
	return hex.EncodeToString(sum[:])
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validateRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("ghacache: repo must be org/name")
	}
	for _, p := range parts {
		if p == "." || p == ".." || strings.ContainsAny(p, `\:`) {
			return fmt.Errorf("ghacache: invalid repo %q", repo)
		}
	}
	return nil
}

func readMeta(path string) (meta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta{}, err
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return meta{}, err
	}
	return m, nil
}

func writeMeta(path string, m meta) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
