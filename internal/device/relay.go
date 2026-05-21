package device

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"
)

const relayBufSize = 32 * 1024

// relayActivity is shared between the two directions of a bidirectional relay
// so the idle timeout only fires when BOTH directions have been silent for
// `idle` duration. A typical case: the SOCKS client has finished uploading
// the request and is just waiting on the response; the upload direction is
// silent, but as long as the download direction is moving bytes we must keep
// the upload side alive (otherwise we'd half-close the target prematurely).
type relayActivity struct {
	lastUnixNano atomic.Int64
}

func newRelayActivity() *relayActivity {
	a := &relayActivity{}
	a.touch()
	return a
}

func (a *relayActivity) touch() {
	a.lastUnixNano.Store(time.Now().UnixNano())
}

func (a *relayActivity) idleFor() time.Duration {
	return time.Since(time.Unix(0, a.lastUnixNano.Load()))
}

func isRelayTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// relayCopy moves data from src to dst with a combined-idle timeout: if act
// is non-nil and shared between both directions, the timer is reset on any
// activity on either side. readTCP is the underlying socket used for read
// deadlines when src is a buffered reader (e.g. bufio).
func relayCopy(dst io.Writer, src io.Reader, readTCP net.Conn, idle time.Duration, act *relayActivity) error {
	if idle <= 0 {
		_, err := io.Copy(dst, src)
		return err
	}

	// Poll with a shorter deadline than `idle` so we can check the shared
	// activity timestamp before declaring the relay dead.
	tick := idle / 4
	if tick > 15*time.Second {
		tick = 15 * time.Second
	}
	if tick < time.Second {
		tick = time.Second
	}

	buf := make([]byte, relayBufSize)
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
		readDeadline := time.Now().Add(tick)
		if readTCP != nil {
			_ = readTCP.SetReadDeadline(readDeadline)
		} else if c, ok := src.(net.Conn); ok {
			_ = c.SetReadDeadline(readDeadline)
		}
		nr, err := src.Read(buf)
		if nr > 0 {
			if act != nil {
				act.touch()
			}
			if dstTCP != nil {
				_ = dstTCP.SetWriteDeadline(time.Now().Add(idle))
			}
			if _, werr := dst.Write(buf[:nr]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// Our own polling tick may have fired. If the OTHER direction
			// has had traffic within `idle` recently, keep going; only
			// declare failure when both sides have been silent that long.
			if isRelayTimeout(err) && act != nil && act.idleFor() <= idle {
				continue
			}
			return err
		}
	}
}
