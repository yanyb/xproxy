package device

import (
	"context"
	"net"
	"strings"
	"time"
)

// targetDialAttemptTimeout caps a single connect attempt to a target IP.
const targetDialAttemptTimeout = 5 * time.Second

type HostLookup func(ctx context.Context, host string) ([]string, error)

func ParseLookupLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func tuneOutboundTCP(c net.Conn) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	// RST on Close so we don't pile FIN_WAIT1 sockets when the target / network dies.
	_ = tc.SetLinger(0)
	// Detect half-open peer (broken Wi-Fi, dead NAT, peer killed) without waiting for
	// the relay idle timeout to expire.
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     30 * time.Second,
		Interval: 10 * time.Second,
		Count:    3,
	})
}

func dialOneTarget(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, targetDialAttemptTimeout)
	defer cancel()
	return d.DialContext(attemptCtx, network, addr)
}

func dialTCP(ctx context.Context, network, address string, lookup HostLookup) (net.Conn, error) {
	var d net.Dialer
	if lookup == nil {
		conn, err := dialOneTarget(ctx, &d, network, address)
		if err != nil {
			return nil, err
		}
		tuneOutboundTCP(conn)
		return conn, nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	ips = preferIPv4First(ips)
	var last error
	for _, ip := range ips {
		conn, err := dialOneTarget(ctx, &d, network, net.JoinHostPort(ip, port))
		if err == nil {
			tuneOutboundTCP(conn)
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func preferIPv4First(ips []string) []string {
	var v4, rest []string
	for _, ip := range ips {
		p := net.ParseIP(ip)
		if p != nil && p.To4() != nil {
			v4 = append(v4, ip)
		} else {
			rest = append(rest, ip)
		}
	}
	return append(v4, rest...)
}
