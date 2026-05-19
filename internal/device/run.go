package device

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"xproxy/internal/config"
	"xproxy/internal/protocol"
)

func Run(ctx context.Context, cfg *config.Device, log *log.Logger) error {
	return RunWith(ctx, cfg, log, nil)
}

func RunWith(ctx context.Context, cfg *config.Device, log *log.Logger, opts *Options) error {
	backoff := cfg.ReconnectMin
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := session(ctx, cfg, log, opts)
		if errors.Is(err, context.Canceled) {
			return err
		}
		if err != nil {
			log.Printf("session ended: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < cfg.ReconnectMax {
			backoff *= 2
			if backoff > cfg.ReconnectMax {
				backoff = cfg.ReconnectMax
			}
		}
	}
}

func session(ctx context.Context, cfg *config.Device, log *log.Logger, opts *Options) error {
	var lookup HostLookup
	var netType func() string
	if opts != nil {
		lookup = opts.Lookup
		netType = opts.NetType
	}

	conn, err := dialTLS(ctx, cfg.ServerAddr, lookup)
	if err != nil {
		return err
	}
	defer conn.Close()

	yc := yamux.DefaultConfig()
	yc.EnableKeepAlive = true
	sess, err := yamux.Client(conn, yc)
	if err != nil {
		return err
	}
	defer sess.Close()

	stream, err := sess.OpenStream()
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type:     protocol.TypeRegister,
		DeviceID: cfg.DeviceID,
	}); err != nil {
		return err
	}

	br := bufio.NewReader(stream)
	ack, err := protocol.ReadLine(br)
	if err != nil {
		return err
	}
	if ack.Type != protocol.TypeRegisterAck || !ack.OK {
		return fmt.Errorf("register rejected: %s", ack.Message)
	}
	log.Printf("registered as %s", cfg.DeviceID)

	errCh := make(chan error, 1)
	go func() { errCh <- heartbeatLoop(ctx, stream, br, cfg.HeartbeatInterval, netType) }()
	go func() { errCh <- acceptConnects(ctx, sess, log, lookup) }()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func dialTLS(ctx context.Context, addr string, lookup HostLookup) (net.Conn, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	if lookup == nil {
		d := &tls.Dialer{Config: tlsCfg}
		return d.DialContext(ctx, "tcp", addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	tlsCfg.ServerName = host
	d := &tls.Dialer{Config: tlsCfg}
	var last error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func heartbeatLoop(ctx context.Context, w io.Writer, br *bufio.Reader, every time.Duration, netType func() string) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			env := &protocol.Envelope{
				Type:  protocol.TypeHeartbeat,
				CurTs: time.Now().UnixMilli(),
			}
			if netType != nil {
				env.NetType = netType()
			}
			if err := protocol.WriteLine(w, env); err != nil {
				return err
			}
			ack, err := protocol.ReadLine(br)
			if err != nil {
				return err
			}
			if ack.Type != protocol.TypeHeartbeatAck {
				return fmt.Errorf("unexpected %s", ack.Type)
			}
		}
	}
}

func acceptConnects(ctx context.Context, sess *yamux.Session, log *log.Logger, lookup HostLookup) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stream, err := sess.AcceptStream()
		if err != nil {
			return err
		}
		go handleConnect(ctx, stream, lookup)
	}
}

func handleConnect(ctx context.Context, stream net.Conn, lookup HostLookup) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	env, err := protocol.ReadLine(br)
	if err != nil || env.Type != protocol.TypeConnect {
		return
	}
	target, err := dialTCP(ctx, env.Network, env.Address, lookup)
	if err != nil {
		_ = protocol.WriteLine(stream, &protocol.Envelope{
			Type:    protocol.TypeConnectResult,
			ID:      env.ID,
			OK:      false,
			Message: err.Error(),
		})
		return
	}
	defer target.Close()

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type: protocol.TypeConnectResult,
		ID:   env.ID,
		OK:   true,
	}); err != nil {
		return
	}

	errCh := make(chan error, 2)
	go func() { _, e := io.Copy(target, br); errCh <- e }()
	go func() { _, e := io.Copy(stream, target); errCh <- e }()
	<-errCh
}
