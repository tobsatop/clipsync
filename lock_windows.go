//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// acquireInstanceLock holds a named mutex for as long as this process lives,
// so only one clipsync runs per user session.
func acquireInstanceLock(string) (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Local\clipsync-instance`)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, errAlreadyRunning
		}
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}
