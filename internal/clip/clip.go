// Package clip is the clipboard as clipsync sees it: a change counter, the
// current content, and a way to replace it.
package clip

import (
	"bytes"
	"context"
	"errors"
	"sync"
)

// Item is one copy. It can hold text, an image, or both: some apps put both
// on the clipboard (spreadsheet cells, for example) and the app you paste
// into picks the one it understands.
type Item struct {
	Text  []byte // UTF-8 with \n line endings; nil if none
	Image []byte // PNG; nil if none
}

func (it Item) Empty() bool { return len(it.Text) == 0 && len(it.Image) == 0 }

func (it Item) clone() Item {
	return Item{Text: append([]byte(nil), it.Text...), Image: append([]byte(nil), it.Image...)}
}

var (
	// ErrNothing means the clipboard holds nothing clipsync can send.
	ErrNothing = errors.New("nothing to sync on the clipboard")
	// ErrSensitive means a password manager marked the content as secret.
	ErrSensitive = errors.New("clipboard content is marked sensitive")
	// ErrFiles means files were copied (as in Finder or Explorer), which
	// clipsync does not send.
	ErrFiles = errors.New("files were copied")
	// ErrBusy means the clipboard listed nothing, which happens both when it
	// is empty and when another app holds it; worth a quiet retry.
	ErrBusy = errors.New("clipboard busy or empty")
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
	failReads int // test hook: make the next reads fail
	failErr   error
}

// FailReads makes the next n reads return err, like a clipboard held by
// another app.
func (m *Memory) FailReads(n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failReads, m.failErr = n, err
}

func NewMemory() *Memory { return &Memory{} }

// Copy simulates the user copying something.
func (m *Memory) Copy(it Item, sensitive bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.item = it.clone()
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
	if m.failReads > 0 {
		m.failReads--
		return Item{}, m.failErr
	}
	if !m.has {
		return Item{}, ErrNothing
	}
	if m.sensitive {
		return Item{}, ErrSensitive
	}
	return m.item.clone(), nil
}

func (m *Memory) Write(_ context.Context, it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.item = it.clone()
	m.has = true
	m.sensitive = false
	m.writes++
	return nil
}
