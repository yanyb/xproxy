package socks

import (
	"net"
	"sync"
)

// clientLimiter caps concurrent active SOCKS-to-device tunnels both globally
// and per device. It is consulted only after the SOCKS handshake completes
// (in DialAndRequest); accept-side back-pressure is handled separately by the
// global semaphore on the listener.
type clientLimiter struct {
	mu           sync.Mutex
	perDevice    map[string]int
	maxPerDevice int
	totalSlots   chan struct{} // global cap; nil if unlimited
}

func newClientLimiter(globalMax, perDevice int) *clientLimiter {
	l := &clientLimiter{
		perDevice:    make(map[string]int),
		maxPerDevice: perDevice,
	}
	if globalMax > 0 {
		l.totalSlots = make(chan struct{}, globalMax)
	}
	return l
}

// acquire reserves slots for a (global, deviceID) pair. Returns nil if either
// quota is exhausted. The returned release func must be called exactly once
// (use countedConn.Close).
func (l *clientLimiter) acquire(deviceID string) func() {
	// Global slot first (cheaper to fail fast on overloaded server).
	if l.totalSlots != nil {
		select {
		case l.totalSlots <- struct{}{}:
		default:
			return nil
		}
	}

	if l.maxPerDevice > 0 {
		l.mu.Lock()
		if l.perDevice[deviceID] >= l.maxPerDevice {
			l.mu.Unlock()
			if l.totalSlots != nil {
				<-l.totalSlots
			}
			return nil
		}
		l.perDevice[deviceID]++
		l.mu.Unlock()
	}

	return func() {
		if l.maxPerDevice > 0 {
			l.mu.Lock()
			if l.perDevice[deviceID] > 0 {
				l.perDevice[deviceID]--
			}
			if l.perDevice[deviceID] == 0 {
				delete(l.perDevice, deviceID)
			}
			l.mu.Unlock()
		}
		if l.totalSlots != nil {
			<-l.totalSlots
		}
	}
}

// countedConn wraps a tunnel conn so closing it releases the limiter slot.
type countedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func newCountedConn(c net.Conn, release func()) net.Conn {
	return &countedConn{Conn: c, release: release}
}

func (c *countedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}
