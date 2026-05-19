package socks

import (
	"context"
	"log"
	"net"
	"time"
	"xproxy/internal/config"
	"xproxy/internal/tunnel"

	"github.com/things-go/go-socks5"
)

type Server struct {
	cfg *config.Server
	s5  *socks5.Server
	ln  net.Listener
}

func New(cfg *config.Server, reg *tunnel.Registry, log *log.Logger) *Server {
	dialOpts := tunnel.DialOpts{
		Registry:    reg,
		DeviceWait:  cfg.DeviceWait,
		ConnectWait: cfg.ConnectWait,
		Log:         log,
	}

	opts := []socks5.Option{
		socks5.WithLogger(socks5.NewLogger(log)),
		socks5.WithRule(&socks5.PermitCommand{EnableConnect: true}),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, req *socks5.Request) (net.Conn, error) {
			deviceID, err := reg.ResolveDevice(username(req))
			if err != nil {
				return nil, err
			}
			return tunnel.Dial(ctx, dialOpts, deviceID, network, addr)
		}),
	}

	if cfg.SocksPassword != "" {
		opts = append(opts, socks5.WithCredential(credential{password: cfg.SocksPassword}))
	}

	return &Server{cfg: cfg, s5: socks5.NewServer(opts...)}
}

func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.SocksListen)
	if err != nil {
		return err
	}
	s.ln = &lingerListener{Listener: ln}
	return nil
}

// lingerListener tunes accepted SOCKS client TCP sockets (linger, keepalive).
type lingerListener struct {
	net.Listener
}

func (l *lingerListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		tuneSOCKSClientTCP(tc)
	}
	return c, nil
}

func tuneSOCKSClientTCP(tc *net.TCPConn) {
	// RST on final Close(), avoids long FIN_WAIT1 when the peer never ACKs FIN.
	_ = tc.SetLinger(0)
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
