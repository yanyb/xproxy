package socks5

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/things-go/go-socks5/statute"
)

// ctxKey is an unexported type for per-request context keys. Using an
// unexported type prevents accidental collisions with keys set by other
// packages (resolvers, middlewares, etc.).
type ctxKey int

const (
	ctxKeyUID ctxKey = iota
	ctxKeyDst
)

// withRequestID attaches a fresh request id (uid) and the current destination
// string (dst) to ctx. Used by logErrf to tag each log line so the lifecycle
// of a single SOCKS request can be followed end-to-end.
func withRequestID(ctx context.Context, dst string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyUID, uuid.New().String())
	ctx = context.WithValue(ctx, ctxKeyDst, dst)
	return ctx
}

// withDst refreshes the dst tag on ctx, preserving the uid. Call after an
// address rewrite changes the final destination.
func withDst(ctx context.Context, dst string) context.Context {
	return context.WithValue(ctx, ctxKeyDst, dst)
}

// logPrefix renders the uid/dst stored on ctx as a short log prefix like
// "[uid=ab12... dst=example.com:443] ". Returns "" if neither value is set.
func logPrefix(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	uid, _ := ctx.Value(ctxKeyUID).(string)
	dst, _ := ctx.Value(ctxKeyDst).(string)
	if uid == "" && dst == "" {
		return ""
	}
	return fmt.Sprintf("[uid=%s dst=%s] ", uid, dst)
}

// AddressRewriter is used to rewrite a destination transparently
type AddressRewriter interface {
	Rewrite(ctx context.Context, request *Request) (context.Context, *statute.AddrSpec)
}

// A Request represents request received by a server
type Request struct {
	statute.Request
	// AuthContext provided during negotiation
	AuthContext *AuthContext
	// LocalAddr of the network server listen
	LocalAddr net.Addr
	// RemoteAddr of the network that sent the request
	RemoteAddr net.Addr
	// DestAddr of the actual destination (might be affected by rewrite)
	DestAddr *statute.AddrSpec
	// Reader connect of request
	Reader io.Reader
	// TCPConn is the SOCKS client socket (for deadlines when Reader is buffered).
	TCPConn net.Conn
	// RawDestAddr of the desired destination
	RawDestAddr *statute.AddrSpec
}

// ParseRequest creates a new Request from the tcp connection
func ParseRequest(bufConn io.Reader) (*Request, error) {
	hd, err := statute.ParseRequest(bufConn)
	if err != nil {
		return nil, err
	}
	return &Request{
		Request:     hd,
		RawDestAddr: &hd.DstAddr,
		Reader:      bufConn,
	}, nil
}

// handleRequest is used for request processing after authentication
func (sf *Server) handleRequest(write io.Writer, req *Request) error {
	var err error

	// Tag the request as early as possible so every subsequent log line shares
	// the same uid. dst starts as the raw client request and gets refreshed
	// after FQDN resolve / rewrite.
	ctx := withRequestID(context.Background(), req.RawDestAddr.String())

	// Resolve the address if we have a FQDN
	dest := req.RawDestAddr
	if dest.FQDN != "" {
		ctx, dest.IP, err = sf.resolver.Resolve(ctx, dest.FQDN)
		if err != nil {
			if err := SendReply(write, statute.RepHostUnreachable, nil); err != nil {
				sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
				return err
			}
			sf.logger.Errorf(logPrefix(ctx)+"failed to resolve destination[%v], %v", dest.FQDN, err)
			return err
		}
	}

	// Apply any address rewrites
	req.DestAddr = req.RawDestAddr
	if sf.rewriter != nil {
		ctx, req.DestAddr = sf.rewriter.Rewrite(ctx, req)
	}
	// Refresh dst now that the final destination is known (post-resolve + post-rewrite).
	ctx = withDst(ctx, req.DestAddr.String())

	// Check if this is allowed
	var ok bool
	ctx, ok = sf.rules.Allow(ctx, req)
	if !ok {
		if err := SendReply(write, statute.RepRuleFailure, nil); err != nil {
			sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
			return err
		}
		sf.logger.Errorf(logPrefix(ctx)+"bind to %v blocked by rules", req.RawDestAddr)
		return fmt.Errorf("bind to %v blocked by rules", req.RawDestAddr)
	}

	var last Handler
	// Switch on the command
	switch req.Command {
	case statute.CommandConnect:
		last = sf.handleConnect
		if sf.userConnectHandle != nil {
			last = sf.userConnectHandle
		}
		if len(sf.userConnectMiddlewares) != 0 {
			return sf.userConnectMiddlewares.Execute(ctx, write, req, last)
		}
	case statute.CommandBind:
		last = sf.handleBind
		if sf.userBindHandle != nil {
			last = sf.userBindHandle
		}
		if len(sf.userBindMiddlewares) != 0 {
			return sf.userBindMiddlewares.Execute(ctx, write, req, last)
		}
	case statute.CommandAssociate:
		last = sf.handleAssociate
		if sf.userAssociateHandle != nil {
			last = sf.userAssociateHandle
		}
		if len(sf.userAssociateMiddlewares) != 0 {
			return sf.userAssociateMiddlewares.Execute(ctx, write, req, last)
		}
	default:
		if err := SendReply(write, statute.RepCommandNotSupported, nil); err != nil {
			sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
			return err
		}
		sf.logger.Errorf(logPrefix(ctx)+"unsupported command[%v]", req.Command)
		return fmt.Errorf("unsupported command[%v]", req.Command)
	}
	return last(ctx, write, req)
}

// handleConnect is used to handle a connect command
func (sf *Server) handleConnect(ctx context.Context, writer io.Writer, request *Request) error {
	// Attempt to connect
	var target net.Conn
	var err error

	if sf.dialWithRequest != nil {
		target, err = sf.dialWithRequest(ctx, "tcp", request.DestAddr.String(), request)
	} else {
		dial := sf.dial
		if dial == nil {
			dial = func(ctx context.Context, net_, addr string) (net.Conn, error) {
				return net.Dial(net_, addr) // nolint: noctx
			}
		}
		target, err = dial(ctx, "tcp", request.DestAddr.String())
	}
	if err != nil {
		msg := err.Error()
		resp := statute.RepHostUnreachable
		if strings.Contains(msg, "refused") {
			resp = statute.RepConnectionRefused
		} else if strings.Contains(msg, "network is unreachable") {
			resp = statute.RepNetworkUnreachable
		}
		if err := SendReply(writer, resp, nil); err != nil {
			sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
			return err
		}
		sf.logger.Errorf(logPrefix(ctx)+"connect to %v failed, %v", request.RawDestAddr, err)
		return err
	}
	defer target.Close() // nolint: errcheck

	// Send success
	if err := SendReply(writer, statute.RepSuccess, target.LocalAddr()); err != nil {
		sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
		return err
	}

	// Start proxying (both directions must finish; idle timeout per read/write).
	errCh := make(chan error, 2)
	sf.goFunc(func() { errCh <- sf.Proxy(ctx, target, request.Reader, request.TCPConn) })
	sf.goFunc(func() { errCh <- sf.Proxy(ctx, writer, target, target) })
	var firstErr error
	for i := 0; i < 2; i++ {
		if e := <-errCh; e != nil && firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}

// handleBind is used to handle a connect command
func (sf *Server) handleBind(ctx context.Context, writer io.Writer, _ *Request) error {
	// TODO: Support bind
	if err := SendReply(writer, statute.RepCommandNotSupported, nil); err != nil {
		sf.logger.Errorf(logPrefix(ctx)+"failed to send reply: %v", err)
		return err
	}
	return nil
}

// handleAssociate is used to handle a connect command
func (sf *Server) handleAssociate(ctx context.Context, writer io.Writer, request *Request) error {
	// Attempt to connect
	dial := sf.dial
	if dial == nil {
		dial = func(_ context.Context, net_, addr string) (net.Conn, error) {
			return net.Dial(net_, addr) // nolint: noctx
		}
	}

	var udpAddr *net.UDPAddr
	if sf.useBindIpBaseResolveAsUdpAddr {
		if sf.bindIP != nil {
			var err error
			udpAddr, err = net.ResolveUDPAddr("udp", sf.bindIP.String()+":0")
			if err != nil {
				if err := SendReply(writer, statute.RepServerFailure, nil); err != nil {
					sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
					return err
				}
				sf.logger.Errorf(logPrefix(ctx)+"failed to resolve udp addr, %v", err)
				return err
			}
		}
	} else {
		tcpAddr, ok := request.LocalAddr.(*net.TCPAddr)
		if !ok {
			if err := SendReply(writer, statute.RepServerFailure, nil); err != nil {
				sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
				return err
			}
			sf.logger.Errorf(logPrefix(ctx)+"local address is not TCP: %T", request.LocalAddr)
			return fmt.Errorf("local address is not TCP: %T", request.LocalAddr)
		}
		udpAddr = &net.UDPAddr{IP: tcpAddr.IP, Port: 0}
	}
	bindLn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		if err := SendReply(writer, statute.RepServerFailure, nil); err != nil {
			sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
			return err
		}
		sf.logger.Errorf(logPrefix(ctx)+"listen udp failed, %v", err)
		return err
	}

	sf.logger.Errorf(logPrefix(ctx)+"client want to used addr %v, listen addr: %s", request.DestAddr, bindLn.LocalAddr())
	// send BND.ADDR and BND.PORT, client used
	if err = SendReply(writer, statute.RepSuccess, bindLn.LocalAddr()); err != nil {
		sf.logger.Errorf(logPrefix(ctx)+"failed to send reply, %v", err)
		return err
	}

	sf.goFunc(func() {
		// read from client and write to remote server
		conns := sync.Map{}
		bufPool := sf.bufferPool.Get()
		defer func() {
			sf.bufferPool.Put(bufPool)
			bindLn.Close() // nolint: errcheck
			conns.Range(func(key, value any) bool {
				if connTarget, ok := value.(net.Conn); !ok {
					sf.logger.Errorf(logPrefix(ctx)+"conns has illegal item %v:%v", key, value)
				} else {
					connTarget.Close() // nolint: errcheck
				}
				return true
			})
		}()
		for {
			n, srcAddr, err := bindLn.ReadFromUDP(bufPool[:cap(bufPool)])
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			pk, err := statute.ParseDatagram(bufPool[:n])
			if err != nil {
				continue
			}

			// check src addr whether equal requst.DestAddr
			srcEqual := ((request.DestAddr.IP.IsUnspecified()) || request.DestAddr.IP.Equal(srcAddr.IP)) && (request.DestAddr.Port == 0 || request.DestAddr.Port == srcAddr.Port) //nolint:lll
			if !srcEqual {
				continue
			}

			connKey := srcAddr.String() + "--" + pk.DstAddr.String()

			if target, ok := conns.Load(connKey); !ok {
				// if the 'connection' doesn't exist, create one and store it
				targetNew, err := dial(ctx, "udp", pk.DstAddr.String())
				if err != nil {
					sf.logger.Errorf(logPrefix(ctx)+"connect to %v failed, %v", pk.DstAddr, err)
					// TODO:continue or return Error?
					continue
				}
				conns.Store(connKey, targetNew)
				// read from remote server and write to original client
				sf.goFunc(func() {
					bufPool := sf.bufferPool.Get()
					defer func() {
						targetNew.Close() // nolint: errcheck
						conns.Delete(connKey)
						sf.bufferPool.Put(bufPool)
					}()

					for {
						buf := bufPool[:cap(bufPool)]
						n, err := targetNew.Read(buf)
						if err != nil {
							if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
								return
							}
							sf.logger.Errorf(logPrefix(ctx)+"read data from remote %s failed, %v", addrString(targetNew.RemoteAddr()), err)
							return
						}
						tmpBufPool := sf.bufferPool.Get()
						proBuf := tmpBufPool
						proBuf = append(proBuf, pk.Header()...)
						proBuf = append(proBuf, buf[:n]...)
						if _, err := bindLn.WriteTo(proBuf, srcAddr); err != nil {
							sf.bufferPool.Put(tmpBufPool)
							sf.logger.Errorf(logPrefix(ctx)+"write data to client %s failed, %v", srcAddr, err)
							return
						}
						sf.bufferPool.Put(tmpBufPool)
					}
				})
				if _, err := targetNew.Write(pk.Data); err != nil {
					sf.logger.Errorf(logPrefix(ctx)+"write data to remote server %s failed, %v", addrString(targetNew.RemoteAddr()), err)
					return
				}
			} else {
				conn, ok := target.(net.Conn)
				if !ok {
					sf.logger.Errorf(logPrefix(ctx)+"invalid connection type in pool: %T", target)
					return
				}
				if _, err := conn.Write(pk.Data); err != nil {
					sf.logger.Errorf(logPrefix(ctx)+"write data to remote server %s failed, %v", addrString(conn.RemoteAddr()), err)
					return
				}
			}
		}
	})

	buf := sf.bufferPool.Get()
	defer sf.bufferPool.Put(buf)

	for {
		_, err := request.Reader.Read(buf[:cap(buf)])
		// sf.logger.Errorf("read data from client %s, %d bytesm, err is %+v", request.RemoteAddr.String(), num, err)
		if err != nil {
			bindLn.Close() // nolint: errcheck
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

// SendReply is used to send a reply message
// rep: reply status see statute's statute file
func SendReply(w io.Writer, rep uint8, bindAddr net.Addr) error {
	rsp := statute.Reply{
		Version:  statute.VersionSocks5,
		Response: rep,
		BndAddr: statute.AddrSpec{
			AddrType: statute.ATYPIPv4,
			IP:       net.IPv4zero,
			Port:     0,
		},
	}

	if rsp.Response == statute.RepSuccess {
		if tcpAddr, ok := bindAddr.(*net.TCPAddr); ok && tcpAddr != nil {
			rsp.BndAddr.IP = tcpAddr.IP
			rsp.BndAddr.Port = tcpAddr.Port
		} else if udpAddr, ok := bindAddr.(*net.UDPAddr); ok && udpAddr != nil {
			rsp.BndAddr.IP = udpAddr.IP
			rsp.BndAddr.Port = udpAddr.Port
		} else {
			rsp.Response = statute.RepAddrTypeNotSupported
		}

		if rsp.BndAddr.IP.To4() != nil {
			rsp.BndAddr.AddrType = statute.ATYPIPv4
		} else if rsp.BndAddr.IP.To16() != nil {
			rsp.BndAddr.AddrType = statute.ATYPIPv6
		}
	}
	// Send the message
	_, err := w.Write(rsp.Bytes())
	return err
}

type closeWriter interface {
	CloseWrite() error
}

// addrString returns the string representation of a net.Addr, or "<nil>" if the address is nil.
func addrString(addr net.Addr) string {
	if addr == nil {
		return "<nil>"
	}
	return addr.String()
}

// Proxy relays src to dst. readTCP sets read deadlines when src is a buffered
// client reader. ctx carries per-request uid/dst for log correlation; it is
// not used for cancellation here (relay lifetime is bounded by idle timeout).
func (sf *Server) Proxy(ctx context.Context, dst io.Writer, src io.Reader, readTCP net.Conn) error {
	buf := sf.bufferPool.Get()
	defer sf.bufferPool.Put(buf)

	var err error
	if sf.proxyIdleTimeout > 0 {
		err = sf.proxyTransfer(ctx, dst, src, readTCP, buf[:cap(buf)])
	} else {
		_, err = io.CopyBuffer(dst, src, buf[:cap(buf)])
	}
	if tcpConn, ok := dst.(closeWriter); ok {
		tcpConn.CloseWrite() //nolint: errcheck
	}
	return err
}

func (sf *Server) proxyTransfer(ctx context.Context, dst io.Writer, src io.Reader, readTCP net.Conn, buf []byte) error {
	idle := sf.proxyIdleTimeout
	dstTCP, _ := dst.(net.Conn)

	clearDeadline := func() {
		if readTCP != nil {
			_ = readTCP.SetReadDeadline(time.Time{})
		}
		if dstTCP != nil {
			_ = dstTCP.SetWriteDeadline(time.Time{})
		}
	}
	defer clearDeadline()

	for {
		deadline := time.Now().Add(idle)
		if readTCP != nil {
			_ = readTCP.SetReadDeadline(deadline)
		} else if c, ok := src.(net.Conn); ok {
			_ = c.SetReadDeadline(deadline)
		}
		nr, err := src.Read(buf)
		if nr > 0 {
			if dstTCP != nil {
				_ = dstTCP.SetWriteDeadline(deadline)
			}
			if _, werr := dst.Write(buf[:nr]); werr != nil {
				sf.logger.Errorf(logPrefix(ctx)+"write to dst %v", werr)
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			sf.logger.Errorf(logPrefix(ctx)+"read from src %v", err)
			return err
		}
	}
}
