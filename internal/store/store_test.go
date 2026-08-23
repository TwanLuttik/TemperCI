package store_test

import (
	"path/filepath"
	"testing"
	"time"

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

func TestWebhookDeliveryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.LastWebhookDelivery()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected no delivery, got %+v", got)
	}

	at := time.Date(2026, 8, 22, 21, 0, 0, 0, time.UTC)
	if err := s.RecordWebhookDelivery(store.WebhookDelivery{
		At:       at,
		Event:    "ping",
		Delivery: "abc-123",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.LastWebhookDelivery()
	if err != nil || got == nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if got.Event != "ping" || got.Delivery != "abc-123" || !got.At.Equal(at) {
		t.Fatalf("delivery = %+v", got)
	}

	later := at.Add(time.Minute)
	if err := s.RecordWebhookDelivery(store.WebhookDelivery{
		At:       later,
		Event:    "workflow_job",
		Delivery: "def-456",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.LastWebhookDelivery()
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Event != "workflow_job" || got.Delivery != "def-456" {
		t.Fatalf("updated delivery = %+v", got)
	}
}
