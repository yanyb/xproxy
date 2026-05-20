package tunnel

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"sync"
	"time"
	"xproxy/internal/protocol"

	"github.com/hashicorp/yamux"
)

// deviceRegisterBudget caps TLS handshake + yamux open + register exchange so a
// half-open client cannot pin a goroutine indefinitely.
const deviceRegisterBudget = 15 * time.Second

func ServeDevice(conn net.Conn, reg *Registry, heartbeatTTL time.Duration, log *log.Logger) {
	defer conn.Close()

	// Slowloris guard: close the conn if register doesn't complete in time.
	registerTimer := time.AfterFunc(deviceRegisterBudget, func() { _ = conn.Close() })

	sess, err := yamux.Server(conn, yamuxCfg())
	if err != nil {
		registerTimer.Stop()
		log.Printf("device yamux: %v", err)
		return
	}
	defer sess.Close()

	stream, err := sess.AcceptStream()
	if err != nil {
		registerTimer.Stop()
		log.Printf("device accept stream: %v", err)
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
	log.Printf("device %s online", id)

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
				log.Printf("device %s control: %v", id, err)
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

func watchHeartbeat(ctx context.Context, conn net.Conn, ttl time.Duration, mu *sync.Mutex, last *time.Time, log *log.Logger, id string) {
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
				log.Printf("device %s heartbeat timeout", id)
				_ = conn.Close()
				return
			}
		}
	}
}

func yamuxCfg() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 30 * time.Second
	// Cap every yamux write at 10s so a stalled peer can't freeze a relay goroutine.
	c.ConnectionWriteTimeout = 10 * time.Second
	return c
}
