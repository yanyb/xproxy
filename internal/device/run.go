package device

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"xproxy/internal/config"
	"xproxy/internal/protocol"

	"github.com/hashicorp/yamux"
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
		err := session(ctx, cfg, log, opts, func() {
			backoff = cfg.ReconnectMin
		})
		if errors.Is(err, context.Canceled) {
			return err
		}
		if err != nil {
			log.Printf("session ended: %v", err)
			t := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				if !t.Stop() {
					<-t.C
				}
				return ctx.Err()
			case <-t.C:
			}
			if backoff < cfg.ReconnectMax {
				backoff *= 2
				if backoff > cfg.ReconnectMax {
					backoff = cfg.ReconnectMax
				}
			}
		}
	}
}

func session(ctx context.Context, cfg *config.Device, log *log.Logger, opts *Options, onRegistered func()) error {
	var lookup HostLookup
	var netType func() string
	var netChange <-chan struct{}
	if opts != nil {
		lookup = opts.Lookup
		netType = opts.NetType
		if opts.OnNetworkChange != nil {
			netChange = opts.OnNetworkChange()
		}
	}
	// Wrap with a TTL DNS cache (crawler-friendly: repeated lookups of the same
	// host hit the cache). Only meaningful if a Lookup callback is wired (mobile);
	// the standalone device binary lets net.Dialer use the OS resolver directly.
	lookup = withDNSCache(lookup, cfg.DNSCacheTTL)

	conn, err := dialTLS(ctx, cfg.ServerAddr, lookup)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Bound the time between TCP+TLS+register: if the server accepts but never
	// responds we drop the conn instead of waiting on yamux keepalive (~30s).
	registerTimer := time.AfterFunc(registerBudget, func() { _ = conn.Close() })

	yc := yamux.DefaultConfig()
	yc.EnableKeepAlive = true
	yc.KeepAliveInterval = 30 * time.Second
	// Bound every yamux-stream write (incl. CONNECT_ACK, heartbeats) so a frozen
	// uplink can't pin a goroutine waiting for an ACK frame forever.
	yc.ConnectionWriteTimeout = 10 * time.Second
	sess, err := yamux.Client(conn, yc)
	if err != nil {
		registerTimer.Stop()
		return err
	}
	defer sess.Close()

	stream, err := sess.OpenStream()
	if err != nil {
		registerTimer.Stop()
		return err
	}
	defer stream.Close()

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type:     protocol.TypeRegister,
		DeviceID: cfg.DeviceID,
	}); err != nil {
		registerTimer.Stop()
		return err
	}

	br := bufio.NewReader(stream)
	ack, err := protocol.ReadLine(br)
	if err != nil {
		registerTimer.Stop()
		return err
	}
	if ack.Type != protocol.TypeRegisterAck || !ack.OK {
		registerTimer.Stop()
		return fmt.Errorf("register rejected: %s", ack.Message)
	}
	registerTimer.Stop()
	log.Printf("registered as %s", cfg.DeviceID)
	if onRegistered != nil {
		onRegistered()
	}

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// errCh buffer of 2: both goroutines may write once; we only read once here
	// and rely on defers + cancel to unblock the second writer (which still has room).
	errCh := make(chan error, 2)
	go func() {
		errCh <- heartbeatLoop(sessCtx, stream, br, cfg.HeartbeatInterval, netType)
	}()
	go func() {
		errCh <- acceptConnects(sessCtx, sess, lookup, cfg.ProxyIdleTimeout, cfg.MaxConcurrent)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-netChange:
		// netChange is nil when no callback is wired (selects on nil block forever).
		return fmt.Errorf("network changed")
	case err := <-errCh:
		return err
	}
}

// tlsDialAttemptTimeout caps a single TCP+TLS handshake; tighter than kernel SYN retries.
const tlsDialAttemptTimeout = 10 * time.Second

// registerBudget caps the time from "conn opened" to "register ack received".
const registerBudget = 15 * time.Second

// connectEnvelopeBudget caps how long we wait for the server's CONNECT envelope
// on a newly-opened yamux stream. Without this, a server-side stall would pin a
// concurrency slot until yamux keepalive (~30s) tears down the whole session.
const connectEnvelopeBudget = 10 * time.Second

func dialTLS(ctx context.Context, addr string, lookup HostLookup) (net.Conn, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	if lookup == nil {
		return dialTLSOne(ctx, &tls.Dialer{Config: tlsCfg}, addr)
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
		conn, err := dialTLSOne(ctx, d, net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func dialTLSOne(ctx context.Context, d *tls.Dialer, addr string) (net.Conn, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, tlsDialAttemptTimeout)
	defer cancel()
	return d.DialContext(attemptCtx, "tcp", addr)
}

func heartbeatLoop(ctx context.Context, stream net.Conn, br *bufio.Reader, every time.Duration, netType func() string) error {
	t := time.NewTicker(every)
	defer t.Stop()
	// per-heartbeat read deadline: at most one missed beat before declaring the
	// session dead. Tighter than waiting for yamux keepalive (~30s).
	readBudget := 2 * every
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
			if err := protocol.WriteLine(stream, env); err != nil {
				return err
			}
			_ = stream.SetReadDeadline(time.Now().Add(readBudget))
			ack, err := protocol.ReadLine(br)
			_ = stream.SetReadDeadline(time.Time{})
			if err != nil {
				return err
			}
			if ack.Type != protocol.TypeHeartbeatAck {
				return fmt.Errorf("unexpected %s", ack.Type)
			}
		}
	}
}

func acceptConnects(ctx context.Context, sess *yamux.Session, lookup HostLookup, relayIdle time.Duration, maxConcurrent int) error {
	var sem chan struct{}
	if maxConcurrent > 0 {
		sem = make(chan struct{}, maxConcurrent)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stream, err := sess.AcceptStream()
		if err != nil {
			return err
		}
		go func(stream net.Conn) {
			if !acquireRelaySlot(ctx, sem) {
				rejectConnect(stream, "too many concurrent connections")
				return
			}
			defer releaseRelaySlot(sem)
			handleConnect(ctx, stream, lookup, relayIdle)
		}(stream)
	}
}

func acquireRelaySlot(ctx context.Context, sem chan struct{}) bool {
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func releaseRelaySlot(sem chan struct{}) {
	if sem != nil {
		<-sem
	}
}

func rejectConnect(stream net.Conn, message string) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	_ = stream.SetReadDeadline(time.Now().Add(connectEnvelopeBudget))
	env, err := protocol.ReadLine(br)
	_ = stream.SetReadDeadline(time.Time{})
	if err != nil || env.Type != protocol.TypeConnect {
		return
	}
	_ = protocol.WriteLine(stream, &protocol.Envelope{
		Type:    protocol.TypeConnectResult,
		ID:      env.ID,
		OK:      false,
		Message: message,
	})
}

func handleConnect(ctx context.Context, stream net.Conn, lookup HostLookup, relayIdle time.Duration) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	_ = stream.SetReadDeadline(time.Now().Add(connectEnvelopeBudget))
	env, err := protocol.ReadLine(br)
	_ = stream.SetReadDeadline(time.Time{})
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
	var stopTarget sync.Once
	closeTarget := func() { stopTarget.Do(func() { _ = target.Close() }) }
	defer closeTarget()

	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type: protocol.TypeConnectResult,
		ID:   env.ID,
		OK:   true,
	}); err != nil {
		return
	}

	errCh := make(chan error, 2)
	go func() {
		defer closeTarget()
		errCh <- relayCopy(target, br, stream, relayIdle)
	}()
	go func() {
		defer closeTarget()
		errCh <- relayCopy(stream, target, target, relayIdle)
	}()
	for i := 0; i < 2; i++ {
		<-errCh
	}
}
