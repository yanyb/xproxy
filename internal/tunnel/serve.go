package tunnel

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"time"
	"xproxy/internal/protocol"
	"xproxy/internal/xlog"

	"github.com/hashicorp/yamux"
)

// deviceRegisterBudget caps TLS handshake + yamux open + register exchange so a
// half-open client cannot pin a goroutine indefinitely.
const deviceRegisterBudget = 15 * time.Second

func ServeDevice(conn net.Conn, reg *Registry, heartbeatTTL time.Duration, log *xlog.Logger) {
	defer conn.Close()

	tuneCarrierTCP(conn)

	// Slowloris guard: close the conn if register doesn't complete in time.
	registerTimer := time.AfterFunc(deviceRegisterBudget, func() { _ = conn.Close() })

	sess, err := yamux.Server(conn, yamuxCfg())
	if err != nil {
		registerTimer.Stop()
		log.Errorf("device yamux: %v", err)
		return
	}
	defer sess.Close()

	stream, err := sess.AcceptStream()
	if err != nil {
		registerTimer.Stop()
		log.Errorf("device accept stream: %v", err)
		return
	}

	br := bufio.NewReader(stream)
	first, err := protocol.ReadLine(br)
	if err != nil || first.Type != protocol.TypeRegister || first.DeviceID == "" {
		registerTimer.Stop()
		_ = protocol.WriteLine(stream, &protocol.Envelope{Type: protocol.TypeRegisterAck, OK: false, Message: "register required"})
		return
	}
	id := first.DeviceID
	if err := protocol.WriteLine(stream, &protocol.Envelope{Type: protocol.TypeRegisterAck, OK: true}); err != nil {
		registerTimer.Stop()
		return
	}
	registerTimer.Stop()

	reg.Put(id, sess)
	defer reg.Remove(id, sess)
	log.Infof("device %s online", id)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	lastBeat := time.Now()
	if heartbeatTTL > 0 {
		go watchHeartbeat(ctx, conn, heartbeatTTL, &mu, &lastBeat, log, id)
	}

	for {
		env, err := protocol.ReadLine(br)
		if err != nil {
			if err != io.EOF {
				log.Errorf("device %s control: %v", id, err)
			}
			return
		}
		if env.Type != protocol.TypeHeartbeat {
			continue
		}
		mu.Lock()
		lastBeat = time.Now()
		mu.Unlock()
		_ = protocol.WriteLine(stream, &protocol.Envelope{
			Type:  protocol.TypeHeartbeatAck,
			CurTs: env.CurTs,
		})
	}
}

func watchHeartbeat(ctx context.Context, conn net.Conn, ttl time.Duration, mu *sync.Mutex, last *time.Time, log *xlog.Logger, id string) {
	t := time.NewTicker(ttl / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			mu.Lock()
			stale := time.Since(*last) > ttl
			mu.Unlock()
			if stale {
				log.Errorf("device %s heartbeat timeout", id)
				_ = conn.Close()
				return
			}
		}
	}
}

// tuneCarrierTCP unwraps a *tls.Conn (or anything exposing NetConn()) down to
// the *net.TCPConn and applies socket options that matter for the yamux
// carrier: NoDelay (so small control / heartbeat frames aren't held back by
// Nagle) and TCP keepalive (low-level liveness check independent of yamux's
// own app-level keepalive).
func tuneCarrierTCP(c net.Conn) {
	type netConner interface{ NetConn() net.Conn }
	var tc *net.TCPConn
	if nc, ok := c.(netConner); ok {
		tc, _ = nc.NetConn().(*net.TCPConn)
	} else {
		tc, _ = c.(*net.TCPConn)
	}
	if tc == nil {
		return
	}
	_ = tc.SetNoDelay(true)
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     30 * time.Second,
		Interval: 10 * time.Second,
		Count:    3,
	})
}

func yamuxCfg() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 30 * time.Second
	// Cap every yamux write at 10s so a stalled peer can't freeze a relay goroutine.
	c.ConnectionWriteTimeout = 10 * time.Second
	// Match the phone side (run.go). Default 256KB throttles per-stream
	// throughput on high BDP links; 4MB gives headroom without burning RAM.
	c.MaxStreamWindowSize = 4 * 1024 * 1024
	return c
}
