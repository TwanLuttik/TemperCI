package github

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ParseRSAPrivateKeyPEM parses a PKCS#1 or PKCS#8 RSA private key from PEM.
func ParseRSAPrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github: no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: private key is not RSA")
	}
	return key, nil
}

// mintAppJWT creates a short-lived JWT for authenticating as the GitHub App.
// iat is skewed 60s into the past to tolerate clock drift; exp is at most 10 minutes.
func mintAppJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	if appID == 0 {
		return "", errors.New("github: app id is required")
	}
	if key == nil {
		return "", errors.New("github: private key is required")
	}
	claims := jwt.MapClaims{
		"iss": appID,
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("github: sign app jwt: %w", err)
	}
	return signed, nil
}
