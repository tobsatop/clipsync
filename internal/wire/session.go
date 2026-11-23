package wire

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Message types inside an encrypted session.
const (
	MsgAuth  byte = 1
	MsgText  byte = 2
	MsgImage byte = 3
	MsgPing  byte = 4
	MsgAck   byte = 5 // payload: SHA-256 of the item received
)

const writeTimeout = 2 * time.Minute

// ErrBadKey means the other side could not prove it holds the pairing key.
var ErrBadKey = errors.New("login failed: the pairing keys do not match")

// Session is an authenticated, encrypted connection to the paired computer.
//
// Keys come from the pairing key mixed with a fresh X25519 exchange, so
// recorded traffic stays private even if the pairing key leaks later. The
// exact bytes of both Hellos are bound in, each direction has its own
// AES-256-GCM key, and nonces are counters, so messages cannot be replayed,
// reflected or tampered with.
type Session struct {
	conn     net.Conn
	PeerID   string
	PeerPort int
	// Dialed is true when this side opened the connection.
	Dialed bool
	// Started is when the login completed.
	Started time.Time

	sendMu  sync.Mutex
	send    cipher.AEAD
	sendCtr uint64

	recv    cipher.AEAD
	recvCtr uint64 // only touched by the single reader

	closeOnce sync.Once
}

// Dial connects to addr and logs in as a computer paired with peerID.
func Dial(ctx context.Context, addr string, self Identity, peerID string, key []byte) (*Session, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s, err := dialSync(conn, self, peerID, key)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return s, nil
}

func dialSync(conn net.Conn, self Identity, peerID string, key []byte) (*Session, error) {
	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	mine := newHello(ModeSync, self, eph.PublicKey().Bytes())
	if err := sendHello(conn, &mine); err != nil {
		return nil, err
	}
	h, err := ReadHello(conn)
	if err != nil {
		return nil, fmt.Errorf("the other computer closed the connection (is it paired with this one?): %w", err)
	}
	if h.Mode != ModeSync {
		return nil, errors.New("the other computer is not ready to sync")
	}
	if h.ID != peerID {
		return nil, fmt.Errorf("%q answered, which is not the paired computer", h.Name)
	}
	d2l, l2d, err := sessionKeys(key, eph, h.Eph, mine.raw, h.raw)
	if err != nil {
		return nil, err
	}
	return finish(conn, h, true, d2l, l2d)
}

// AcceptSync completes the login for an incoming connection whose Hello has
// been read and whose ID belongs to the paired computer.
func AcceptSync(conn net.Conn, h *Hello, self Identity, key []byte) (*Session, error) {
	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	if h.Mode != ModeSync {
		return nil, errors.New("not a sync request")
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	mine := newHello(ModeSync, self, eph.PublicKey().Bytes())
	if err := sendHello(conn, &mine); err != nil {
		return nil, err
	}
	d2l, l2d, err := sessionKeys(key, eph, h.Eph, h.raw, mine.raw)
	if err != nil {
		return nil, err
	}
	return finish(conn, h, false, d2l, l2d)
}

func finish(conn net.Conn, h *Hello, dialed bool, d2l, l2d []byte) (*Session, error) {
	s := &Session{conn: conn, PeerID: h.ID, PeerPort: h.Port, Dialed: dialed}
	sendKey, recvKey := l2d, d2l
	if dialed {
		sendKey, recvKey = d2l, l2d
	}
	var err error
	if s.send, err = newAEAD(sendKey); err != nil {
		return nil, err
	}
	if s.recv, err = newAEAD(recvKey); err != nil {
		return nil, err
	}
	// Each side proves it holds the key by sending an encrypted auth
	// message that the other must decrypt.
	if err := s.Send(MsgAuth, nil); err != nil {
		return nil, err
	}
	typ, _, err := s.recvFrame(handshakeMax)
	if err != nil {
		if errors.Is(err, ErrBadKey) {
			return nil, err
		}
		return nil, fmt.Errorf("the other computer closed the connection during login (does it have the same pairing?): %w", err)
	}
	if typ != MsgAuth {
		return nil, errors.New("unexpected message during login")
	}
	conn.SetDeadline(time.Time{})
	s.Started = time.Now()
	return s, nil
}

// sessionKeys makes one key per direction from the pairing key, the X25519
// shared secret, and a hash of both Hellos (dialer's first).
func sessionKeys(longterm []byte, mine *ecdh.PrivateKey, theirs, helloD, helloL []byte) (d2l, l2d []byte, err error) {
	pub, err := ecdh.X25519().NewPublicKey(theirs)
	if err != nil {
		return nil, nil, errors.New("malformed hello")
	}
	shared, err := mine.ECDH(pub) // fails on low-order points
	if err != nil {
		return nil, nil, errors.New("malformed hello")
	}
	secret := make([]byte, 0, len(longterm)+len(shared))
	secret = append(secret, longterm...)
	secret = append(secret, shared...)

	th := sha256.New()
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(helloD)))
	th.Write(n[:])
	th.Write(helloD)
	th.Write(helloL)
	transcript := th.Sum(nil)

	if d2l, err = hkdf.Key(sha256.New, secret, transcript, "clipsync session v1 dialer->listener", 32); err != nil {
		return nil, nil, err
	}
	if l2d, err = hkdf.Key(sha256.New, secret, transcript, "clipsync session v1 listener->dialer", 32); err != nil {
		return nil, nil, err
	}
	return d2l, l2d, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// counterNonce gives each message a unique nonce; keys are fresh per session
// and per direction, so a counter is enough.
func counterNonce(ctr uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n
}

// Send encrypts and sends one message. Safe for concurrent use.
func (s *Session) Send(typ byte, payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("item too large (%d bytes)", len(payload))
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	pt := make([]byte, 1+len(payload))
	pt[0] = typ
	copy(pt[1:], payload)
	ct := s.send.Seal(nil, counterNonce(s.sendCtr), pt, nil)
	s.sendCtr++
	s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return writeFrame(s.conn, ct)
}

// Recv waits up to timeout for the next message. Call it from one goroutine.
func (s *Session) Recv(timeout time.Duration) (byte, []byte, error) {
	s.conn.SetReadDeadline(time.Now().Add(timeout))
	return s.recvFrame(MaxPayload + 1 + 16)
}

func (s *Session) recvFrame(max int) (byte, []byte, error) {
	ct, err := readFrame(s.conn, max)
	if err != nil {
		return 0, nil, err
	}
	pt, err := s.recv.Open(nil, counterNonce(s.recvCtr), ct, nil)
	if err != nil {
		return 0, nil, ErrBadKey
	}
	s.recvCtr++
	if len(pt) < 1 {
		return 0, nil, errors.New("empty message")
	}
	return pt[0], pt[1:], nil
}

// RemoteHost is the peer's IP address.
func (s *Session) RemoteHost() string {
	host, _, err := net.SplitHostPort(s.conn.RemoteAddr().String())
	if err != nil {
		return s.conn.RemoteAddr().String()
	}
	return host
}

// PeerAddr is where the peer listens for connections.
func (s *Session) PeerAddr() string { return peerAddr(s.conn, s.PeerPort) }

func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.conn.Close() })
	return err
}
