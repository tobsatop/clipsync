//go:build darwin

package node

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"clipsync/internal/clip"
)

// TestRealMacClipboard drives the real pasteboard. It replaces what is on the
// clipboard, so it only runs when CLIPSYNC_REAL_CLIPBOARD=1.
func TestRealMacClipboard(t *testing.T) {
	if os.Getenv("CLIPSYNC_REAL_CLIPBOARD") != "1" {
		t.Skip("set CLIPSYNC_REAL_CLIPBOARD=1 to run against the real clipboard")
	}
	sys, err := clip.NewSystem()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	key := bytes.Repeat([]byte{9}, 32)
	mem := clip.NewMemory()
	mac := newNode(t, ctx, "aaaa", "bbbb", key, sys)
	win := newNode(t, ctx, "bbbb", "aaaa", key, mem)
	mac.SetPeerAddr(win.ListenAddr())
	defer func() { cancel(); mac.Wait(); win.Wait() }()
	waitFor(t, "connection", func() bool { return mac.Connected() && win.Connected() })

	// Mac → other side
	pbcopy(t, "copied on the real Mac ไทย\nline 2")
	waitFor(t, "Mac copy to arrive", func() bool { return text(mem) == "copied on the real Mac ไทย\nline 2" })

	// other side → Mac
	mem.Copy(clip.Item{Kind: clip.Text, Data: []byte("from Windows\nline 2")}, false)
	waitFor(t, "text on the Mac clipboard", func() bool { return pbpaste(t) == "from Windows\nline 2" })

	// A password-manager copy must stay on the Mac.
	before := mem.Writes()
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", `
		ObjC.import('AppKit');
		var pb = $.NSPasteboard.generalPasteboard;
		pb.clearContents;
		pb.setStringForType($('hunter2'), $.NSPasteboardTypeString);
		pb.setStringForType($(''), $('org.nspasteboard.ConcealedType'));
		'ok'`).CombinedOutput()
	if err != nil {
		t.Fatalf("osascript: %v %s", err, out)
	}
	time.Sleep(700 * time.Millisecond)
	if mem.Writes() != before || strings.Contains(text(mem), "hunter2") {
		t.Fatal("password-manager content was sent")
	}

	// No echo: the item written to the Mac must not come back.
	if mem.Writes() != 1 {
		t.Fatalf("other side written %d times, want 1", mem.Writes())
	}
}

func pbcopy(t *testing.T, s string) {
	t.Helper()
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}

func pbpaste(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
