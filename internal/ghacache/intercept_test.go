package ghacache

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestPeekClientHelloSNI(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	errCh := make(chan error, 1)
	go func() {
		cfg := &tls.Config{ServerName: "results-receiver.actions.githubusercontent.com", InsecureSkipVerify: true}
		cli := tls.Client(c1, cfg)
		errCh <- cli.Handshake()
	}()

	sni, hello, err := PeekClientHello(c2)
	if err != nil {
		t.Fatalf("PeekClientHello: %v", err)
	}
	if sni != "results-receiver.actions.githubusercontent.com" {
		t.Fatalf("sni=%q", sni)
	}
	if len(hello) < 40 {
		t.Fatalf("hello too short: %d", len(hello))
	}
	_ = c2.Close()
	select {
	case <-errCh:
	case <-time.After(time.Second):
	}
}

func TestIntercept_SplicesNonCacheSNI(t *testing.T) {
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backendLn.Close() })
	backendAddr := backendLn.Addr().String()

	var got []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := backendLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 2048)
		n, _ := c.Read(buf)
		got = append([]byte(nil), buf[:n]...)
		_, _ = c.Write([]byte("upstream-ok"))
	}()

	frontLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = frontLn.Close() })

	ix := &Intercept{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not run for spliced SNI")
		}),
		Dial: func(network, address string) (net.Conn, error) {
			if address != "api.github.com:443" {
				t.Errorf("dialed %q", address)
			}
			return net.Dial("tcp", backendAddr)
		},
	}
	go func() { _ = ix.Serve(frontLn) }()

	c, err := net.Dial("tcp", frontLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	cli := tls.Client(c, &tls.Config{ServerName: "api.github.com", InsecureSkipVerify: true})
	_ = cli.SetDeadline(time.Now().Add(2 * time.Second))
	// Handshake will fail (backend is not TLS); we only need the ClientHello forwarded.
	_ = cli.Handshake()
	_ = c.Close()
	wg.Wait()
	if len(got) < 40 || got[0] != 0x16 {
		t.Fatalf("backend did not receive TLS ClientHello: %d bytes %v", len(got), got[:min(8, len(got))])
	}
}

func TestIntercept_TerminatesCacheSNI(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ix := &Intercept{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "cache-hit")
		}),
		CA: ca,
	}
	go func() { _ = ix.Serve(ln) }()

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: "results-receiver.actions.githubusercontent.com",
		},
		Dial: func(network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ln.Addr().String())
		},
	}
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	resp, err := client.Get("https://results-receiver.actions.githubusercontent.com/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "cache-hit" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}

func TestIntercept_ClassifyTerminatesRegistrySNI(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ix := &Intercept{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "oci-hit")
		}),
		CA: ca,
		Classify: func(sni string) bool {
			return sni == "ghcr.io"
		},
	}
	go func() { _ = ix.Serve(ln) }()

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "ghcr.io"},
		Dial: func(network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ln.Addr().String())
		},
	}
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	resp, err := client.Get("https://ghcr.io/v2/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "oci-hit" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}
