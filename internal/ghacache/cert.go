package ghacache

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Authority is a local CA used to terminate intercepted cache TLS.
type Authority struct {
	Cert *x509.Certificate
	Key  crypto.Signer

	mu    sync.Mutex
	leafs map[string]*tls.Certificate
}

// GenerateCA creates a new in-memory TemperCI cache CA.
func GenerateCA() (*Authority, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "TemperCI Actions Cache CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Authority{Cert: cert, Key: key, leafs: make(map[string]*tls.Certificate)}, nil
}

// LoadOrCreateCA loads ca.crt/ca.key from dir, or writes a new pair.
func LoadOrCreateCA(dir string) (*Authority, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	crtPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if _, err := os.Stat(crtPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return loadCA(crtPath, keyPath)
		}
	}
	a, err := GenerateCA()
	if err != nil {
		return nil, err
	}
	if err := writeCA(crtPath, keyPath, a); err != nil {
		return nil, err
	}
	return a, nil
}

func loadCA(crtPath, keyPath string) (*Authority, error) {
	crtPEM, err := os.ReadFile(crtPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(crtPEM)
	if cb == nil {
		return nil, fmt.Errorf("ghacache: invalid ca.crt")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("ghacache: invalid ca.key")
	}
	key, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		pkcs8, err2 := x509.ParsePKCS8PrivateKey(kb.Bytes)
		if err2 != nil {
			return nil, err
		}
		var ok bool
		key, ok = pkcs8.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("ghacache: ca.key is not RSA")
		}
	}
	return &Authority{Cert: cert, Key: key, leafs: make(map[string]*tls.Certificate)}, nil
}

func writeCA(crtPath, keyPath string, a *Authority) error {
	rsaKey, ok := a.Key.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("ghacache: ca key is not RSA")
	}
	if err := os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.Cert.Raw}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}), 0o600)
}

// Certificate returns a leaf TLS cert for host, signed by the CA.
func (a *Authority) Certificate(host string) (*tls.Certificate, error) {
	if a == nil || a.Cert == nil || a.Key == nil {
		return nil, fmt.Errorf("ghacache: missing CA")
	}
	if host == "" {
		host = "localhost"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.leafs == nil {
		a.leafs = make(map[string]*tls.Certificate)
	}
	if c, ok := a.leafs[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.Cert, &key.PublicKey, a.Key)
	if err != nil {
		return nil, err
	}
	leaf := &tls.Certificate{
		Certificate: [][]byte{der, a.Cert.Raw},
		PrivateKey:  key,
	}
	a.leafs[host] = leaf
	return leaf, nil
}
