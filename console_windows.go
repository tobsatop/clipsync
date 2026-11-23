//go:build windows

package main

import (
	"bufio"
	"os"

	"golang.org/x/sys/windows"
)

// The Windows build is a GUI program, so it starts without a console
// window. Commands that print or ask questions get one here.

var (
	kernel32DLL          = windows.NewLazySystemDLL("kernel32.dll")
	procAllocConsole     = kernel32DLL.NewProc("AllocConsole")
	procAttachConsole    = kernel32DLL.NewProc("AttachConsole")
	procFreeConsole      = kernel32DLL.NewProc("FreeConsole")
	procGetConsoleWindow = kernel32DLL.NewProc("GetConsoleWindow")
)

const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS

// ownConsole is true when clipsync opened its own console window (so it
// should pause before the window closes).
var ownConsole bool

// useConsole gives a command a console. Interactive commands get a window
// of their own: cmd.exe does not wait for a GUI program, so sharing its
// console would mean both reading the same keystrokes. Commands that only
// print attach to the console they were started from, if any.
func useConsole(interactive bool) {
	if w, _, _ := procGetConsoleWindow.Call(); w != 0 {
		return // already has one (e.g. a console build)
	}
	if !interactive {
		if r, _, _ := procAttachConsole.Call(attachParentProcess); r != 0 {
			bindConsole()
			return
		}
	}
	if r, _, _ := procAllocConsole.Call(); r == 0 {
		return
	}
	ownConsole = true
	bindConsole()
	setupConsole()
}

func bindConsole() (in, out *os.File) {
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr, out = f, f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin, in = f, f
		stdin = bufio.NewReader(f)
	}
	return in, out
}

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
