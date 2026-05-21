package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"
	"xproxy/internal/xlog"

	"xproxy/internal/protocol"
)

type DialOpts struct {
	Registry    *Registry
	DeviceWait  time.Duration
	ConnectWait time.Duration
	Log         *xlog.Logger
}

func Dial(ctx context.Context, o DialOpts, deviceID, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("only tcp supported")
	}

	waitCtx := ctx
	if o.DeviceWait > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, o.DeviceWait)
		defer cancel()
	}

	log.Println("wait device:", deviceID)

	sess, err := o.Registry.Wait(waitCtx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("wait device: %w", err)
	}

	stream, err := sess.OpenStream()
	if err != nil {
		return nil, err
	}

	log.Println("open stream")

	reqID := randomID()
	if err := protocol.WriteLine(stream, &protocol.Envelope{
		Type:    protocol.TypeConnect,
		ID:      reqID,
		Network: network,
		Address: addr,
	}); err != nil {
		_ = stream.Close()
		return nil, err
	}

	readCtx := ctx
	if o.ConnectWait > 0 {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(ctx, o.ConnectWait)
		defer cancel()
	}

	type result struct {
		env *protocol.Envelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		env, err := protocol.ReadLineFromConn(stream)
		ch <- result{env, err}
	}()

	select {
	case <-readCtx.Done():
		_ = stream.Close()
		return nil, readCtx.Err()
	case r := <-ch:
		if r.err != nil {
			_ = stream.Close()
			return nil, r.err
		}
		if r.env.Type != protocol.TypeConnectResult || r.env.ID != reqID || !r.env.OK {
			_ = stream.Close()
			msg := r.env.Message
			if msg == "" {
				msg = "connect failed"
			}
			return nil, fmt.Errorf("%s", msg)
		}
		log.Println("connect target success:", addr)
		return stream, nil
	}
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
