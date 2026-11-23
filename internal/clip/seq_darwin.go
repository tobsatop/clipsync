//go:build darwin && !ios

package clip

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego/objc"
)

// AppKit is already loaded by golang.design/x/clipboard, so the classes
// resolve once it has initialised; look them up lazily to be safe.
var (
	seqOnce        sync.Once
	clsPasteboard  objc.Class
	clsPool        objc.Class
	selGeneral     objc.SEL
	selChangeCount objc.SEL
	selNew         objc.SEL
	selDrain       objc.SEL
)

// changeCount is NSPasteboard.generalPasteboard.changeCount.
func changeCount() int64 {
	seqOnce.Do(func() {
		clsPasteboard = objc.GetClass("NSPasteboard")
		clsPool = objc.GetClass("NSAutoreleasePool")
		selGeneral = objc.RegisterName("generalPasteboard")
		selChangeCount = objc.RegisterName("changeCount")
		selNew = objc.RegisterName("new")
		selDrain = objc.RegisterName("drain")
	})
	// An autorelease pool must be drained on the thread that made it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(clsPool).Send(selNew)
	defer pool.Send(selDrain)
	return int64(objc.ID(clsPasteboard).Send(selGeneral).Send(selChangeCount))
}
