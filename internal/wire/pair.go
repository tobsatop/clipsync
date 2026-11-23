package wire

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"time"

	"filippo.io/cpace"
)

// Pairing turns a 6-digit code into a strong shared key with CPace, a
// password-authenticated key exchange: someone watching the network learns
// nothing about the code, and each connection can test only one guess. The
// host therefore counts every connection that gets as far as the exchange,
// and gives up after a few, so an attacker gets a handful of guesses out of
// a million.

const (
	pairTimeout = 30 * time.Second
	pairContext = "clipsync pair v1"
)

var (
	ErrWrongCode = errors.New("wrong pairing code")
	// ErrPairAttempt marks a host-side failure that used up one guess.
	ErrPairAttempt = errors.New("pairing attempt failed")
	// ErrTooManyAttempts means the code is burned; show a new one.
	ErrTooManyAttempts = errors.New("too many failed pairing attempts")
	errNotPairing      = errors.New("not a pairing request")
)

type pairMsg struct {
	Pake    []byte `json:"pake,omitempty"`
	Confirm []byte `json:"confirm,omitempty"`
	Fail    bool   `json:"fail,omitempty"`
	OK      bool   `json:"ok,omitempty"`
}

// PairResult is what each side stores about the other.
type PairResult struct {
	PeerID   string
	PeerName string
	PeerAddr string // host:port where the peer listens
	Key      []byte
}

// JoinPair connects to a computer that is showing a code and pairs with it.
func JoinPair(ctx context.Context, addr string, self Identity, code string) (*PairResult, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(pairTimeout))

	mine := newHello(ModePair, self, nil)
	if err := sendHello(conn, &mine); err != nil {
		return nil, err
	}
	h, err := ReadHello(conn)
	if err != nil {
		return nil, err
	}
	if h.Mode != ModePair {
		return nil, errors.New("the other computer is not showing a pairing code")
	}

	msgA, st, err := cpace.Start(code, cpace.NewContextInfo(self.ID, h.ID, []byte(pairContext)))
	if err != nil {
		return nil, err
	}
	if err := writeJSON(conn, pairMsg{Pake: msgA}); err != nil {
		return nil, err
	}
	var m pairMsg
	if err := readJSON(conn, &m); err != nil {
		return nil, err
	}
	shared, err := st.Finish(m.Pake)
	if err != nil {
		return nil, err
	}
	longterm, hostConfirm, joinConfirm, err := pairKeys(shared)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(m.Confirm, hostConfirm) {
		writeJSON(conn, pairMsg{Fail: true})
		return nil, ErrWrongCode
	}
	if err := writeJSON(conn, pairMsg{Confirm: joinConfirm}); err != nil {
		return nil, err
	}
	var ack pairMsg
	if err := readJSON(conn, &ack); err != nil {
		return nil, err
	}
	if !ack.OK {
		return nil, ErrWrongCode
	}
	return &PairResult{PeerID: h.ID, PeerName: h.Name, PeerAddr: peerAddr(conn, h.Port), Key: longterm}, nil
}

// ServePairing accepts pairing requests on ln until one succeeds. Every
// request that reaches the key exchange and does not succeed counts against
// maxAttempts, whatever the reason. onFail is told how many tries are left.
// Close ln to stop it.
func ServePairing(ln net.Listener, self Identity, code string, maxAttempts int, onFail func(left int)) (*PairResult, error) {
	failed := 0
	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil, err
		}
		res, err := servePairingConn(conn, self, code)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, ErrPairAttempt) {
			continue // never reached the exchange, so no guess was tested
		}
		failed++
		if failed >= maxAttempts {
			return nil, ErrTooManyAttempts
		}
		if onFail != nil {
			onFail(maxAttempts - failed)
		}
	}
}

func servePairingConn(conn net.Conn, self Identity, code string) (*PairResult, error) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	h, err := ReadHello(conn)
	if err != nil {
		return nil, err
	}
	if h.Mode != ModePair {
		return nil, errNotPairing
	}
	return HostPair(conn, h, self, code)
}

// HostPair answers a pairing request on a connection whose Hello was read.
// Any failure after the key exchange wraps ErrPairAttempt.
func HostPair(conn net.Conn, h *Hello, self Identity, code string) (*PairResult, error) {
	conn.SetDeadline(time.Now().Add(pairTimeout))
	if h.Mode != ModePair {
		return nil, errNotPairing
	}
	mine := newHello(ModePair, self, nil)
	if err := sendHello(conn, &mine); err != nil {
		return nil, err
	}
	var m pairMsg
	if err := readJSON(conn, &m); err != nil {
		return nil, err
	}
	msgB, shared, err := cpace.Exchange(code, cpace.NewContextInfo(h.ID, self.ID, []byte(pairContext)), m.Pake)
	if err != nil {
		return nil, err
	}
	// From here on the peer may learn whether its guess was right.
	attempt := func(err error) error { return fmt.Errorf("%w: %w", ErrPairAttempt, err) }
	longterm, hostConfirm, joinConfirm, err := pairKeys(shared)
	if err != nil {
		return nil, attempt(err)
	}
	if err := writeJSON(conn, pairMsg{Pake: msgB, Confirm: hostConfirm}); err != nil {
		return nil, attempt(err)
	}
	var c pairMsg
	if err := readJSON(conn, &c); err != nil {
		return nil, attempt(err)
	}
	if c.Fail || !hmac.Equal(c.Confirm, joinConfirm) {
		return nil, attempt(ErrWrongCode)
	}
	if err := writeJSON(conn, pairMsg{OK: true}); err != nil {
		return nil, attempt(err)
	}
	return &PairResult{PeerID: h.ID, PeerName: h.Name, PeerAddr: peerAddr(conn, h.Port), Key: longterm}, nil
}

// pairKeys derives the long-term key and the two confirmation tags that
// prove each side got the same result (i.e. typed the same code).
func pairKeys(shared []byte) (longterm, hostConfirm, joinConfirm []byte, err error) {
	longterm, err = hkdf.Key(sha256.New, shared, nil, "clipsync longterm v1", 32)
	if err != nil {
		return nil, nil, nil, err
	}
	ck, err := hkdf.Key(sha256.New, shared, nil, "clipsync confirm v1", 32)
	if err != nil {
		return nil, nil, nil, err
	}
	tag := func(role string) []byte {
		m := hmac.New(sha256.New, ck)
		m.Write([]byte(role))
		return m.Sum(nil)
	}
	return longterm, tag("host"), tag("joiner"), nil
}
