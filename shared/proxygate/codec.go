package proxygate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"time"
)

// A fixed flat canonical JSON frame, followed by LF. Every field is required.
// Binding is the SHA256 of the entire Policy. Nonces and sessions are generated
// by the receiver, never selected by controller grants.
type message struct {
	Version       int    `json:"version"`
	Binding       string `json:"binding"`
	Session       string `json:"session"`
	Nonce         string `json:"nonce"`
	Sequence      uint64 `json:"sequence"`
	Action        string `json:"action"`
	UntilUnixNano int64  `json:"until_unix_nano"`
}

func validID(s string) bool { return len(s) == 32 && digest(s+s) }
func decodeMessage(b []byte) (message, error) {
	var m message
	if len(b) == 0 || len(b) > MaxMessageBytes {
		return m, ErrProtocol
	}
	if json.Unmarshal(b, &m) != nil || m.Version != Version || !digest(m.Binding) || !validID(m.Session) || !validID(m.Nonce) || m.Sequence > 9007199254740991 {
		return message{}, ErrProtocol
	}
	switch m.Action {
	case "hello", "closed", "grant", "ack":
	default:
		return message{}, ErrProtocol
	}
	if m.UntilUnixNano < 0 {
		return message{}, ErrProtocol
	}
	canonical, _ := json.Marshal(m)
	if !bytes.Equal(b, canonical) {
		return message{}, ErrProtocol
	}
	return m, nil
}
func readMessage(c net.Conn, r *bufio.Reader, deadline time.Time) (message, error) {
	if c.SetReadDeadline(deadline) != nil {
		return message{}, ErrProtocol
	}
	line, e := r.ReadSlice('\n')
	if e != nil || len(line) > MaxMessageBytes+1 {
		return message{}, ErrProtocol
	}
	return decodeMessage(line[:len(line)-1])
}
func writeMessage(c net.Conn, m message, deadline time.Time) error {
	b, e := json.Marshal(m)
	if e != nil || len(b) > MaxMessageBytes {
		return ErrProtocol
	}
	b = append(b, '\n')
	if c.SetWriteDeadline(deadline) != nil {
		return ErrProtocol
	}
	for len(b) > 0 {
		n, e := c.Write(b)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
