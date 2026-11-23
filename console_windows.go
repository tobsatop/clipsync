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

// useConsole gives a command a console: the one it was started from, or a
// new window.
func useConsole() {
	if w, _, _ := procGetConsoleWindow.Call(); w != 0 {
		return // already has one (e.g. a console build)
	}
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		if r, _, _ := procAllocConsole.Call(); r == 0 {
			return
		}
	}
	bindConsole()
}

// openConsole opens a new console window for a while (pairing from the
// tray) and returns a function that closes it.
func openConsole() (func(), error) {
	if r, _, err := procAllocConsole.Call(); r == 0 {
		return nil, err
	}
	oldIn, oldOut, oldErr, oldReader := os.Stdin, os.Stdout, os.Stderr, stdin
	in, out := bindConsole()
	setupConsole()
	return func() {
		os.Stdin, os.Stdout, os.Stderr, stdin = oldIn, oldOut, oldErr, oldReader
		if in != nil {
			in.Close()
		}
		if out != nil {
			out.Close()
		}
		procFreeConsole.Call()
	}, nil
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
