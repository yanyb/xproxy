package device

import (
	"context"
	"net"
	"strings"
)

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

func dialTCP(ctx context.Context, network, address string, lookup HostLookup) (net.Conn, error) {
	if lookup == nil {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	var last error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}
