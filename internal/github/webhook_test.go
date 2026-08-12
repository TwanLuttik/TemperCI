package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyWebhookSignature_Valid(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"action":"queued"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := VerifyWebhookSignature(secret, body, sig); err != nil {
		t.Fatalf("expected valid signature, got %v", err)
	}
}

func TestVerifyWebhookSignature_Invalid(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"action":"queued"}`)

	err := VerifyWebhookSignature(secret, body, "sha256=deadbeef")
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestVerifyWebhookSignature_MissingPrefix(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{}`)
	err := VerifyWebhookSignature(secret, body, "not-a-signature")
	if err == nil {
		t.Fatal("expected error for missing sha256= prefix")
	}
}

func TestVerifyWebhookSignature_EmptySecret(t *testing.T) {
	err := VerifyWebhookSignature(nil, []byte(`{}`), "sha256=abc")
	if err == nil {
		t.Fatal("expected error for empty secret")
	}
}
