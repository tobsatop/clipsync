// Package clip is the clipboard as clipsync sees it: a change counter, the
// current content, and a way to replace it.
package clip

import (
	"bytes"
	"context"
	"errors"
	"sync"
)

type Kind byte

const (
	Text  Kind = 1
	Image Kind = 2
)

func (k Kind) String() string {
	switch k {
	case Text:
		return "text"
	case Image:
		return "image"
	}
	return "unknown"
}

// Item is one piece of clipboard content. Text is UTF-8 with \n line endings.
type Item struct {
	Kind Kind
	Data []byte
}

var (
	// ErrNothing means the clipboard holds nothing clipsync can send.
	ErrNothing = errors.New("nothing to sync on the clipboard")
	// ErrSensitive means a password manager marked the content as secret.
	ErrSensitive = errors.New("clipboard content is marked sensitive")
)

type Clipboard interface {
	// Seq changes whenever the clipboard content changes. It is cheap, so it
	// can be polled often.
	Seq() int64
	Read(ctx context.Context) (Item, error)
	Write(ctx context.Context, it Item) error
}

// NormalizeText turns Windows line endings into \n and drops NUL bytes
// (Windows text cannot hold them), so both computers see the same bytes.
// It is idempotent: "a\r\r\nb" becomes "a\nb", not "a\r\nb".
func NormalizeText(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte{0}, nil)
	for bytes.Contains(b, []byte("\r\n")) {
		b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	}
	return b
}

// toCRLF is what Windows apps expect.
func toCRLF(b []byte) []byte {
	return bytes.ReplaceAll(NormalizeText(b), []byte("\n"), []byte("\r\n"))
}

// Memory is an in-process clipboard for tests.
type Memory struct {
	mu        sync.Mutex
	seq       int64
	item      Item
	has       bool
	sensitive bool
	writes    int
}

func NewMemory() *Memory { return &Memory{} }

// Copy simulates the user copying something.
func (m *Memory) Copy(it Item, sensitive bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.item = Item{Kind: it.Kind, Data: append([]byte(nil), it.Data...)}
	m.has = true
	m.sensitive = sensitive
}

// Current returns what is on the clipboard now.
func (m *Memory) Current() (Item, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.item, m.has
}

// Writes counts how many times clipsync wrote to this clipboard.
func (m *Memory) Writes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}

func (m *Memory) Seq() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seq
}

func (m *Memory) Read(context.Context) (Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.has {
		return Item{}, ErrNothing
	}
	if m.sensitive {
		return Item{}, ErrSensitive
	}
	return Item{Kind: m.item.Kind, Data: append([]byte(nil), m.item.Data...)}, nil
}

func (m *Memory) Write(_ context.Context, it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.item = Item{Kind: it.Kind, Data: append([]byte(nil), it.Data...)}
	m.has = true
	m.sensitive = false
	m.writes++
	return nil
}
