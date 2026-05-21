package socks

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
	"xproxy/internal/config"
	"xproxy/internal/tunnel"
	"xproxy/internal/xlog"

	"github.com/things-go/go-socks5"
)

type Server struct {
	cfg     *config.Server
	s5      *socks5.Server
	ln      net.Listener
	limiter *clientLimiter
}

func New(cfg *config.Server, reg *tunnel.Registry, log *xlog.Logger) *Server {
	dialOpts := tunnel.DialOpts{
		Registry:    reg,
		DeviceWait:  cfg.DeviceWait,
		ConnectWait: cfg.ConnectWait,
		Log:         log,
	}

	limiter := newClientLimiter(cfg.MaxClients, cfg.MaxClientsPerDevice)

	opts := []socks5.Option{
		socks5.WithLogger(log),
		socks5.WithRule(&socks5.PermitCommand{EnableConnect: true}),
		socks5.WithProxyIdleTimeout(cfg.ProxyIdleTimeout),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, req *socks5.Request) (net.Conn, error) {
			// SOCKS handshake completed; clear the slowloris guard so relay can
			// be long-lived. Works regardless of whether TCPConn is a raw
			// *net.TCPConn or our semConn wrapper (net.Conn always exposes this).
			if req.TCPConn != nil {
				_ = req.TCPConn.SetReadDeadline(time.Time{})
			}
			deviceID, err := reg.ResolveDevice(username(req))
			if err != nil {
				return nil, err
			}
			release := limiter.acquire(deviceID)
			if release == nil {
				return nil, fmt.Errorf("device %s: too many concurrent clients", deviceID)
			}
			conn, err := tunnel.Dial(ctx, dialOpts, deviceID, network, addr)
			if err != nil {
				release()
				return nil, err
			}
			return newCountedConn(conn, release), nil
		}),
	}

	if cfg.SocksPassword != "" {
		opts = append(opts, socks5.WithCredential(credential{password: cfg.SocksPassword}))
	}

	// Default: pass FQDN to phone (remote DNS). Set socks_local_resolve: true for server DNS.
	if !cfg.SocksLocalResolve {
		opts = append(opts, socks5.WithResolver(remoteResolver{}))
	}

	return &Server{cfg: cfg, s5: socks5.NewServer(opts...), limiter: limiter}
}

func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.SocksListen)
	if err != nil {
		return err
	}
	wrapped := &lingerListener{Listener: ln}
	if s.cfg.MaxClients > 0 {
		// Accept-side cap protects against runaway clients before the SOCKS handshake.
		// Use 4x the in-flight cap to allow brief bursts; reject excess.
		wrapped.acceptSem = make(chan struct{}, s.cfg.MaxClients*4)
	}
	s.ln = wrapped
	return nil
}

// lingerListener tunes accepted SOCKS client TCP sockets (linger, keepalive)
// and bounds simultaneous half-open connections via acceptSem.
type lingerListener struct {
	net.Listener
	acceptSem chan struct{}
}

// socksHandshakeBudget caps how long a client can take to finish the SOCKS5
// handshake (method negotiation + auth + request). Cleared once the relay starts.
const socksHandshakeBudget = 15 * time.Second

func (l *lingerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.acceptSem != nil {
			select {
			case l.acceptSem <- struct{}{}:
				c = &semConn{Conn: c, sem: l.acceptSem}
			default:
				// Server saturated: drop the conn to relieve SYN backlog. The
				// client gets a TCP RST and should back off.
				_ = c.Close()
				continue
			}
		}
		if tc, ok := underlyingTCP(c); ok {
			tuneSOCKSClientTCP(tc)
			_ = tc.SetReadDeadline(time.Now().Add(socksHandshakeBudget))
		}
		return c, nil
	}
}

func underlyingTCP(c net.Conn) (*net.TCPConn, bool) {
	for {
		switch v := c.(type) {
		case *net.TCPConn:
			return v, true
		case *semConn:
			c = v.Conn
		default:
			return nil, false
		}
	}
}

// semConn releases an accept-side slot when closed.
type semConn struct {
	net.Conn
	sem  chan struct{}
	once sync.Once
}

func (c *semConn) Close() error {
	c.once.Do(func() { <-c.sem })
	return c.Conn.Close()
}

func tuneSOCKSClientTCP(tc *net.TCPConn) {
	// RST on final Close(), avoids long FIN_WAIT1 when the peer never ACKs FIN.
	_ = tc.SetLinger(0)
	// Disable Nagle: SOCKS5 handshake is a sequence of tiny (2-23B) packets
	// that ping-pong with the client. Nagle would inject ~40ms of latency
	// per small write while waiting for the previous ACK.
	_ = tc.SetNoDelay(true)
	// Detect killed clients / expired NAT (e.g. NekoBox force-stopped on Android).
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     30 * time.Second,
		Interval: 10 * time.Second,
		Count:    3,
	})
}

func (s *Server) Serve() error {
	return s.s5.Serve(s.ln)
}

func (s *Server) Close() error {
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

func username(req *socks5.Request) string {
	if req == nil || req.AuthContext == nil || req.AuthContext.Payload == nil {
		return ""
	}
	return req.AuthContext.Payload["username"]
}

type credential struct {
	password string
}

func (c credential) Valid(user, password, _ string) bool {
	return password == c.password && user != ""
}
