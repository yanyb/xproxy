package device

import (
	"errors"
	"io"
	"net"
	"time"
)

const relayBufSize = 32 * 1024

// relayCopy moves data from src to dst with per-read/write idle timeout.
// readTCP is the underlying socket for deadlines when src is buffered (e.g. bufio).
func relayCopy(dst io.Writer, src io.Reader, readTCP net.Conn, idle time.Duration) error {
	if idle <= 0 {
		_, err := io.Copy(dst, src)
		return err
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
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
