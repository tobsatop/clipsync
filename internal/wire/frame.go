// Package wire is the conversation between two clipsync computers: pairing
// with a short code, then an encrypted, authenticated session.
package wire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	appName      = "clipsync"
	protoVersion = 1

	// handshakeMax caps every message sent before the peer is authenticated,
	// so a stranger cannot make us allocate much.
	handshakeMax = 4 << 10
	// MaxPayload caps one clipboard item.
	MaxPayload = 64 << 20
)

// writeFrame sends a 4-byte big-endian length followed by b, in one write.
func writeFrame(w io.Writer, b []byte) error {
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader, max int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if int64(n) > int64(max) {
		return nil, fmt.Errorf("message too large (%d bytes)", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFrame(w, b)
}

func readJSON(r io.Reader, v any) error {
	b, err := readFrame(r, handshakeMax)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
