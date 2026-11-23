//go:build windows

package clip

import (
	"context"
	"errors"
	"runtime"
	"time"
	"unsafe"

	"golang.design/x/clipboard"
	"golang.org/x/sys/windows"
)

// The library's own bitmap reader keeps the clipboard open while it
// converts pixel by pixel, which blocks copy and paste in every other app
// for seconds, and it misreads top-down bitmaps. So clipsync copies the raw
// bitmap out quickly, closes the clipboard, and converts afterwards.

const (
	cfDIB   = 8
	cfDIBV5 = 17

	maxDIBBytes = maxPixels*4 + 4096
)

var (
	user32                         = windows.NewLazySystemDLL("user32.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

func readImageFallback(context.Context) ([]byte, error) {
	raw, err := copyClipboardBitmap()
	if err != nil {
		return nil, err
	}
	img, err := dibToImage(raw)
	if err != nil {
		return nil, err
	}
	return encodePNG(img)
}

// copyClipboardBitmap returns the bytes of CF_DIBV5 (or CF_DIB), holding the
// clipboard open only for the copy.
func copyClipboardBitmap() ([]byte, error) {
	var format uintptr
	for _, f := range []uintptr{cfDIBV5, cfDIB} {
		if r, _, _ := procIsClipboardFormatAvailable.Call(f); r != 0 {
			format = f
			break
		}
	}
	if format == 0 {
		return nil, clipboard.ErrNoData
	}

	// The clipboard belongs to the thread that opened it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil, errors.New("the app that copied the image could not provide it")
	}
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 || size > maxDIBBytes {
		return nil, errors.New("copied image is too large")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, errors.New("cannot read the copied image")
	}
	defer procGlobalUnlock.Call(h)
	// p is OS memory (not Go's heap), valid while locked; reinterpreting
	// the variable keeps vet's uintptr check quiet.
	mem := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(mem), size))
	return out, nil
}

func openClipboard() error {
	deadline := time.Now().Add(time.Second)
	for {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the clipboard is busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
