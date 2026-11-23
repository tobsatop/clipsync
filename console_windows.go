//go:build windows

package main

import "golang.org/x/sys/windows"

// setupConsole turns off QuickEdit in the classic Windows console: with it
// on, a stray click starts a text selection that freezes all output until
// Esc is pressed.
func setupConsole() {
	h, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return // not a console (e.g. redirected)
	}
	mode &^= windows.ENABLE_QUICK_EDIT_MODE
	mode |= windows.ENABLE_EXTENDED_FLAGS
	windows.SetConsoleMode(h, mode)
}
