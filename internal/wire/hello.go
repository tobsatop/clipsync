package wire

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"clipsync/internal/names"
)

const (
	ModeSync = "sync"
	ModePair = "pair"

	handshakeTimeout = 10 * time.Second
)

// ErrVersion means the two computers run incompatible clipsync versions.
var ErrVersion = errors.New("the two computers run different clipsync versions — update both")

// Identity is how this computer introduces itself.
type Identity struct {
	ID   string
	Name string
	Port int // TCP port this computer listens on
}

// Hello is the first, unencrypted message each side sends. Nothing in it is
// trusted until the handshake that follows succeeds; for sync, the exact
// bytes of both Hellos are bound into the session keys, so tampering with
// any field makes the login fail.
type Hello struct {
	App  string `json:"app"`
	V    int    `json:"v"`
	Mode string `json:"mode"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
	// Eph is a fresh X25519 public key (sync only), for forward secrecy.
	Eph []byte `json:"eph,omitempty"`

	raw []byte // the bytes as sent or received
}

func newHello(mode string, self Identity, eph []byte) Hello {
	return Hello{App: appName, V: protoVersion, Mode: mode, ID: self.ID, Name: self.Name, Port: self.Port, Eph: eph}
}

// sendHello writes h and remembers the exact bytes.
func sendHello(conn net.Conn, h *Hello) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	h.raw = b
	return writeFrame(conn, b)
}

// ReadHello reads and sanity-checks the peer's Hello. Name comes back
// cleaned for display. On a version mismatch it returns the Hello too, with
// an error wrapping ErrVersion.
func ReadHello(conn net.Conn) (*Hello, error) {
	b, err := readFrame(conn, handshakeMax)
	if err != nil {
		return nil, err
	}
	var h Hello
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, errors.New("not a clipsync computer")
	}
	if h.App != appName {
		return nil, errors.New("not a clipsync computer")
	}
	if h.V != protoVersion {
		h.Name = names.Clean(h.Name)
		return &h, fmt.Errorf("%w (%q uses protocol %d, this computer %d)", ErrVersion, h.Name, h.V, protoVersion)
	}
	if h.ID == "" || len(h.ID) > 128 || h.Port < 0 || h.Port > 65535 {
		return nil, errors.New("malformed hello")
	}
	h.raw = b
	h.Name = names.Clean(h.Name)
	return &h, nil
}

// SendVersionHello answers a peer with a different version, so it can tell
// its user to update instead of guessing at a network problem.
func SendVersionHello(conn net.Conn, self Identity) {
	h := newHello(ModeSync, self, nil)
	sendHello(conn, &h)
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return b
}

// peerAddr is where the peer listens: the IP it connected from and the port
// it announced.
func peerAddr(conn net.Conn, port int) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
