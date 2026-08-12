package store_test

import (
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/store"
)

func TestUsersAndSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	done, err := s.SetupCompleted()
	if err != nil || done {
		t.Fatalf("setup completed = %v err=%v", done, err)
	}
	if err := s.SetSetupCompleted(true); err != nil {
		t.Fatal(err)
	}
	done, err = s.SetupCompleted()
	if err != nil || !done {
		t.Fatalf("setup completed = %v err=%v", done, err)
	}

	u, err := s.CreateUser("Admin@Example.com", "secret-pass", store.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "admin@example.com" || u.Role != store.RoleAdmin {
		t.Fatalf("user = %+v", u)
	}

	got, err := s.Authenticate("admin@example.com", "secret-pass")
	if err != nil || got.ID != u.ID {
		t.Fatalf("auth = %+v err=%v", got, err)
	}
	if _, err := s.Authenticate("admin@example.com", "wrong"); err == nil {
		t.Fatal("expected bad password error")
	}

	tok, _, err := s.CreateSession(u.ID, 0)
	if err != nil || tok == "" {
		t.Fatalf("session = %q err=%v", tok, err)
	}
	su, err := s.SessionUser(tok)
	if err != nil || su == nil || su.ID != u.ID {
		t.Fatalf("session user = %+v err=%v", su, err)
	}
	if err := s.DeleteSession(tok); err != nil {
		t.Fatal(err)
	}
	su, err = s.SessionUser(tok)
	if err != nil || su != nil {
		t.Fatalf("after delete session user = %+v", su)
	}
}
