package protocol

import (
	"bufio"
	"encoding/json"
	"io"
)

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
