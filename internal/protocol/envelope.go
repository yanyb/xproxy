package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

const maxEnvelopeBytes = 64 * 1024

const (
	TypeRegister      = "register"
	TypeRegisterAck   = "register_ack"
	TypeHeartbeat     = "heartbeat"
	TypeHeartbeatAck  = "heartbeat_ack"
	TypeConnect       = "connect"
	TypeConnectResult = "connect_result"
)

type Envelope struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	OK       bool   `json:"ok,omitempty"`
	Message  string `json:"message,omitempty"`
	Network  string `json:"network,omitempty"`
	Address  string `json:"address,omitempty"`
	CurTs    int64  `json:"cur_ts,omitempty"`
	NetType  string `json:"net_type,omitempty"`
}

func WriteLine(w io.Writer, v *Envelope) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func ReadLine(r *bufio.Reader) (*Envelope, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// ReadLineFromConn reads one envelope line byte-by-byte from r so no bytes are
// buffered past the trailing '\n'. Use when subsequent bytes on the same stream
// must remain readable by another reader (e.g. after CONNECT ack on a yamux stream).
func ReadLineFromConn(r io.Reader) (*Envelope, error) {
	var line []byte
	var buf [1]byte
	for {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		if buf[0] == '\n' {
			break
		}
		line = append(line, buf[0])
		if len(line) > maxEnvelopeBytes {
			return nil, errors.New("envelope too long")
		}
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
