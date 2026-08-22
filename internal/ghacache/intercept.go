package ghacache

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Intercept is a TLS SNI multiplexer: cache hosts are terminated and served
// by Handler; every other SNI is spliced to the real destination:443.
type Intercept struct {
	Handler http.Handler
	CA      *Authority
	// Dial is used for splice. Nil uses net.DialTimeout (10s).
	Dial func(network, address string) (net.Conn, error)
}

// PeekClientHello reads a TLS ClientHello from c and returns the SNI plus the
// raw bytes that were consumed (so the caller can replay them).
func PeekClientHello(c net.Conn) (sni string, hello []byte, err error) {
	var buf bytes.Buffer
	var captured string
	hs := tls.Server(&roConn{r: io.TeeReader(c, &buf)}, &tls.Config{
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			captured = info.ServerName
			return nil, errStopHello
		},
	})
	_ = hs.Handshake()
	if buf.Len() == 0 {
		return "", nil, fmt.Errorf("ghacache: empty client hello")
	}
	return captured, buf.Bytes(), nil
}

var errStopHello = errors.New("hello captured")

// Serve accepts connections on ln until it is closed.
func (ix *Intercept) Serve(ln net.Listener) error {
	if ix.Handler == nil {
		return fmt.Errorf("ghacache: intercept handler is nil")
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go ix.handle(c)
	}
}

// ListenAndServe listens on addr and serves intercept/splice.
func (ix *Intercept) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ix.Serve(ln)
}

func (ix *Intercept) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	sni, hello, err := PeekClientHello(c)
	if err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	if !ShouldIntercept(sni) {
		ix.splice(c, hello, sni)
		return
	}
	if ix.CA == nil {
		return
	}
	prefixed := &prefixConn{Conn: c, r: io.MultiReader(bytes.NewReader(hello), c)}
	tlsConn := tls.Server(prefixed, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := info.ServerName
			if host == "" {
				host = sni
			}
			return ix.CA.Certificate(host)
		},
	})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	srv := &http.Server{Handler: ix.Handler, ReadHeaderTimeout: 10 * time.Second}
	_ = srv.Serve(&oneConnListener{conn: tlsConn})
}

func (ix *Intercept) splice(c net.Conn, hello []byte, sni string) {
	dial := ix.Dial
	if dial == nil {
		dial = func(network, address string) (net.Conn, error) {
			return net.DialTimeout(network, address, 10*time.Second)
		}
	}
	addr := sni + ":443"
	if sni == "" {
		if dest, err := originalDest(c); err == nil && dest != "" {
			addr = dest
		} else {
			return
		}
	}
	up, err := dial("tcp", addr)
	if err != nil {
		return
	}
	defer up.Close()
	if _, err := up.Write(hello); err != nil {
		return
	}
	errc := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); errc <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); errc <- struct{}{} }()
	<-errc
}

type roConn struct{ r io.Reader }

func (c *roConn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c *roConn) Write(p []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (c *roConn) Close() error                       { return nil }
func (c *roConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *roConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (c *roConn) SetDeadline(t time.Time) error      { return nil }
func (c *roConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *roConn) SetWriteDeadline(t time.Time) error { return nil }

type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type oneConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() {
		l.done = make(chan struct{})
		c = &notifyClose{Conn: l.conn, done: l.done}
	})
	if c != nil {
		return c, nil
	}
	if l.done != nil {
		<-l.done
	}
	return nil, net.ErrClosed
}
func (l *oneConnListener) Close() error {
	if l.done != nil {
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
	return nil
}
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

type notifyClose struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *notifyClose) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.done) })
	return err
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "tcp" }
func (dummyAddr) String() string  { return "peek" }
