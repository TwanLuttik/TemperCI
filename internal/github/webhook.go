package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// VerifyWebhookSignature checks GitHub's X-Hub-Signature-256 header
// (HMAC-SHA256 of the raw body using the webhook secret).
func VerifyWebhookSignature(secret, body []byte, signatureHeader string) error {
	if len(secret) == 0 {
		return errors.New("github: webhook secret is empty")
	}
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return errors.New("github: missing or invalid X-Hub-Signature-256 prefix")
	}
	gotHex := strings.TrimPrefix(signatureHeader, prefix)
	got, err := hex.DecodeString(gotHex)
	if err != nil {
		return fmt.Errorf("github: invalid signature encoding: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return errors.New("github: webhook signature mismatch")
	}
	return nil
}
