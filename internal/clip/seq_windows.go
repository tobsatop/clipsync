//go:build windows

package clip

import "golang.org/x/sys/windows"

var procGetClipboardSequenceNumber = windows.NewLazySystemDLL("user32.dll").NewProc("GetClipboardSequenceNumber")

// changeCount is GetClipboardSequenceNumber, which Windows bumps on every
// clipboard change and which needs no clipboard lock to read.
func changeCount() int64 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return int64(r)
}
