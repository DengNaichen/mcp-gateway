package main

import (
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// Use the content background behind standard macOS title bars, including the
// updater's window. Keep the title bar's layout and native window controls.
func configureWindowChrome() func() {
	transparent := objc.RegisterName("setTitlebarAppearsTransparent:")
	separator := objc.RegisterName("setTitlebarSeparatorStyle:")
	responds := objc.RegisterName("respondsToSelector:")
	return mygo.App.OnWindowCreated(func(w *mygo.Window) {
		handle := w.NativeHandle()
		if handle == 0 {
			return
		}
		window := objc.ID(handle)
		window.Send(transparent, true)
		// NSTitlebarSeparatorStyleNone (macOS 11+).
		if objc.Send[bool](window, responds, separator) {
			window.Send(separator, uintptr(1))
		}
	})
}
