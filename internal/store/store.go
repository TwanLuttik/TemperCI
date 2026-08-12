// Package store provides SQLite persistence for the operator dashboard
// (users, sessions, setup metadata).
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// Role values for dashboard users.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// User is a local dashboard account.
type User struct {
	ID                 int64
	Email              string
	PasswordHash       string
	Role               string
	MustChangePassword bool
	CreatedAt          time.Time
}

// Store is a SQLite-backed dashboard store.
type Store struct {
	db *sql.DB
}

// Open opens or creates the SQLite database at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// SetupCompleted reports whether setup was marked complete in the DB.
func (s *Store) SetupCompleted() (bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'setup_completed'`).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == "true" || v == "1", nil
}

// SetSetupCompleted sets the setup completion flag.
func (s *Store) SetSetupCompleted(done bool) error {
	val := "false"
	if done {
		val = "true"
	}
	_, err := s.db.Exec(`
INSERT INTO meta(key, value) VALUES('setup_completed', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
`, val)
	return err
}

// CreateUser inserts a user with a bcrypt password hash.
func (s *Store) CreateUser(email, password, role string, mustChange bool) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return nil, fmt.Errorf("store: email and password required")
	}
	if role != RoleAdmin && role != RoleViewer {
		role = RoleViewer
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	mc := 0
	if mustChange {
		mc = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO users(email, password_hash, role, must_change_password, created_at) VALUES(?,?,?,?,?)`,
		email, string(hash), role, mc, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("store: create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return &User{
		ID:                 id,
		Email:              email,
		PasswordHash:       string(hash),
		Role:               role,
		MustChangePassword: mustChange,
		CreatedAt:          now,
	}, nil
}

// UserCount returns the number of users.
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// Authenticate checks email/password and returns the user.
func (s *Store) Authenticate(email, password string) (*User, error) {
	u, err := s.GetUserByEmail(email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("store: invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("store: invalid credentials")
	}
	return u, nil
}

// GetUserByEmail loads a user by email, or nil.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	row := s.db.QueryRow(
		`SELECT id, email, password_hash, role, must_change_password, created_at FROM users WHERE email = ?`,
		email,
	)
	return scanUser(row)
}

// GetUserByID loads a user by id, or nil.
func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, password_hash, role, must_change_password, created_at FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

// ListUsers returns all users (no password hashes exposed beyond struct field — caller should strip).
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, email, password_hash, role, must_change_password, created_at FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUserRows(rows)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = ""
		out = append(out, *u)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanUser(row scannable) (*User, error) {
	var u User
	var mc int
	var created string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &mc, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.MustChangePassword = mc != 0
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, nil
}

func scanUserRows(rows *sql.Rows) (*User, error) {
	return scanUser(rows)
}

// CreateSession issues a new session token (raw token returned once).
func (s *Store) CreateSession(userID int64, ttl time.Duration) (rawToken string, expires time.Time, err error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	rawToken = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])
	expires = time.Now().UTC().Add(ttl)
	now := time.Now().UTC()
	_, err = s.db.Exec(
		`INSERT INTO sessions(id, user_id, token_hash, expires_at, created_at) VALUES(?,?,?,?,?)`,
		tokenHash, userID, tokenHash, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", time.Time{}, err
	}
	return rawToken, expires, nil
}

// SessionUser resolves a raw session token to a user, or nil if invalid/expired.
func (s *Store) SessionUser(rawToken string) (*User, error) {
	if rawToken == "" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])
	var userID int64
	var expStr string
	err := s.db.QueryRow(
		`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash,
	).Scan(&userID, &expStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339Nano, expStr)
	if err != nil || time.Now().UTC().After(exp) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return nil, nil
	}
	return s.GetUserByID(userID)
}

// DeleteSession removes a session by raw token.
func (s *Store) DeleteSession(rawToken string) error {
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}
