//go:build darwin

package node

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
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
	defer savePasteboard(t)()
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
	mem.Copy(clip.Item{Text: []byte("from Windows\nline 2")}, false)
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

// The real-clipboard tests replace what the user copied, so each one saves
// every item and type on the pasteboard right before it starts and puts them
// back when it ends.

const generalPB = "$.NSPasteboard.generalPasteboard"

func savePasteboardJS(pb, path string) string {
	return `var pb=` + pb + `; var items=pb.pasteboardItems; var out=$.NSMutableArray.array;
		for (var i=0; i<items.count; i++) { var it=items.objectAtIndex(i); var types=it.types; var pairs=$.NSMutableArray.array;
			for (var j=0; j<types.count; j++) { var t=types.objectAtIndex(j); var d=it.dataForType(t);
				if (!d.isNil()) { pairs.addObject(t); pairs.addObject(d); } }
			out.addObject(pairs); }
		if (!out.writeToFileAtomically('` + path + `', true)) { throw 'could not save the clipboard'; }`
}

func restorePasteboardJS(pb, path string) string {
	return `var pb=` + pb + `; var arr=$.NSArray.arrayWithContentsOfFile('` + path + `');
		if (arr.isNil()) { throw 'no saved clipboard'; }
		pb.clearContents; var objs=$.NSMutableArray.array;
		for (var i=0; i<arr.count; i++) { var pairs=arr.objectAtIndex(i); var pi=$.NSPasteboardItem.alloc.init;
			for (var j=0; j+1<pairs.count; j+=2) { pi.setDataForType(pairs.objectAtIndex(j+1), pairs.objectAtIndex(j)); }
			objs.addObject(pi); }
		if (objs.count > 0) { pb.writeObjects(objs); }`
}

// savePasteboard snapshots the user's clipboard; call the result to restore.
func savePasteboard(t *testing.T) (restore func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clipboard.plist")
	osa(t, savePasteboardJS(generalPB, path))
	return func() { osa(t, restorePasteboardJS(generalPB, path)) }
}

// TestPasteboardSnapshot checks the snapshot scripts on a private pasteboard,
// so it never touches the user's clipboard.
func TestPasteboardSnapshot(t *testing.T) {
	pb := "$.NSPasteboard.pasteboardWithName('clipsync-snapshot-test')"
	path := filepath.Join(t.TempDir(), "pb.plist")
	osa(t, `var pb=`+pb+`; pb.clearContents; var a=$.NSPasteboardItem.alloc.init;
		a.setStringForType($('สวัสดี text'), $.NSPasteboardTypeString); a.setStringForType($('<b>html</b>'), $.NSPasteboardTypeHTML);
		var b=$.NSPasteboardItem.alloc.init; b.setStringForType($('second'), $.NSPasteboardTypeString);
		pb.writeObjects($([a, b]))`)
	osa(t, savePasteboardJS(pb, path))
	osa(t, `var pb=`+pb+`; pb.clearContents; pb.setStringForType($('junk'), $.NSPasteboardTypeString)`)
	osa(t, restorePasteboardJS(pb, path))
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", `ObjC.import('AppKit'); var pb=`+pb+`;
		var it=pb.pasteboardItems; var s=[];
		for (var i=0;i<it.count;i++){ var x=it.objectAtIndex(i); s.push(x.stringForType($.NSPasteboardTypeString).js + '|' + (x.stringForType($.NSPasteboardTypeHTML).isNil() ? '' : x.stringForType($.NSPasteboardTypeHTML).js)); }
		pb.releaseGlobally; s.join(';')`).CombinedOutput()
	if err != nil {
		t.Fatalf("osascript: %v %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "สวัสดี text|<b>html</b>;second|" {
		t.Fatalf("restored %q", got)
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

func osa(t *testing.T, js string) {
	t.Helper()
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", "ObjC.import('AppKit');"+js+";'ok'").CombinedOutput()
	if err != nil {
		t.Fatalf("osascript: %v %s", err, out)
	}
}

func TestRealMacImages(t *testing.T) {
	if os.Getenv("CLIPSYNC_REAL_CLIPBOARD") != "1" {
		t.Skip("set CLIPSYNC_REAL_CLIPBOARD=1 to run against the real clipboard")
	}
	defer savePasteboard(t)()
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

	dir := t.TempDir()
	pic := testPNG(t, 120, 80, color.RGBA{0, 128, 255, 255})
	path := dir + "/pic.png"
	if err := os.WriteFile(path, pic, 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. A PNG on the Mac arrives byte for byte.
	osa(t, `var pb=$.NSPasteboard.generalPasteboard; pb.clearContents;
		pb.setDataForType($.NSData.dataWithContentsOfFile('`+path+`'), $.NSPasteboardTypePNG)`)
	waitFor(t, "PNG from the Mac", func() bool { it, _ := mem.Current(); return bytes.Equal(it.Image, pic) })

	// 2. A TIFF-only image (as some Mac apps copy) arrives as a valid PNG.
	tiffSrc := dir + "/pic2.png"
	if err := os.WriteFile(tiffSrc, testPNG(t, 50, 40, color.RGBA{255, 200, 0, 255}), 0o600); err != nil {
		t.Fatal(err)
	}
	osa(t, `var pb=$.NSPasteboard.generalPasteboard; var img=$.NSImage.alloc.initWithContentsOfFile('`+tiffSrc+`');
		pb.clearContents; pb.setDataForType(img.TIFFRepresentation, $.NSPasteboardTypeTIFF)`)
	waitFor(t, "TIFF from the Mac as PNG", func() bool {
		it, _ := mem.Current()
		w, h := clip.ImageSize(it.Image)
		return w == 50 && h == 40
	})

	// 3. An image from the other side lands on the Mac clipboard, no echo.
	before := mem.Writes()
	other := testPNG(t, 33, 44, color.RGBA{0, 200, 0, 255})
	mem.Copy(clip.Item{Image: other}, false)
	waitFor(t, "image on the Mac clipboard", func() bool {
		out, _ := exec.Command("osascript", "-e", "clipboard info").CombinedOutput()
		return strings.Contains(string(out), "PNGf")
	})
	got, err := sys.Read(context.Background())
	if err != nil || !bytes.Equal(got.Image, other) {
		t.Fatalf("Mac clipboard image differs: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if mem.Writes() != before {
		t.Fatal("image echoed back to the sender")
	}

	// 4. Copied files are not sent.
	before = mem.Writes()
	osa(t, `var pb=$.NSPasteboard.generalPasteboard; pb.clearContents;
		pb.writeObjects($([$.NSURL.fileURLWithPath('`+path+`')]))`)
	time.Sleep(700 * time.Millisecond)
	if mem.Writes() != before {
		t.Fatal("a file copy was sent")
	}
}
