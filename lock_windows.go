//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// acquireInstanceLock holds a named mutex for as long as this process lives,
// so only one clipsync runs per user session.
func acquireInstanceLock(string) (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Local\clipsync-instance`)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil { // ERROR_ALREADY_EXISTS: another clipsync holds it
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}
