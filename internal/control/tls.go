package control

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSFiles configures optional HTTPS and mTLS for the control-plane listener.
//
// MVP path: shared bearer token over plain HTTP (dev) or HTTPS (TLSCertFile+TLSKeyFile).
// Hardening path: set TLSClientCAFile to require and verify agent client certificates (mTLS)
// in addition to the bearer token.
type TLSFiles struct {
	// CertFile and KeyFile enable HTTPS when both are non-empty.
	CertFile string
	KeyFile  string
	// ClientCAFile, when set with Cert/Key, requires client certificates signed by this CA.
	ClientCAFile string
}

// Enabled reports whether server TLS should be used.
func (t TLSFiles) Enabled() bool {
	return t.CertFile != "" && t.KeyFile != ""
}

// ServerTLSConfig builds a *tls.Config for the control-plane HTTP server.
// Returns nil,nil when TLS is not configured.
func (t TLSFiles) ServerTLSConfig() (*tls.Config, error) {
	if !t.Enabled() {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("control: load TLS cert/key: %w", err)
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if t.ClientCAFile != "" {
		pem, err := os.ReadFile(t.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("control: read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("control: no certificates in client CA file")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
