package socks

import (
	"context"
	"net"
)

// remoteResolver leaves FQDN unresolved on the SOCKS server so the phone egress
// can resolve (socks5h / ATYP domain). Resolve returns no IP; AddrSpec.String()
// keeps hostname:port for tunnel.Dial.
type remoteResolver struct{}

func (remoteResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	_ = name
	return ctx, nil, nil
}
