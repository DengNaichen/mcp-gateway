package main

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

// Keep common upstream brands available while market assets load or the network
// is offline. Additional services use the shared market icon cache.
//
//go:embed assets/services/*.png
var serviceIconFiles embed.FS

var serviceIcons = func() map[string]ui.ImageSource {
	icons := make(map[string]ui.ImageSource)
	for _, id := range []string{"github", "linear"} {
		data, err := serviceIconFiles.ReadFile("assets/services/" + id + ".png")
		if err != nil {
			panic(err)
		}
		img, err := ui.DecodeImage(data)
		if err != nil {
			panic(err)
		}
		icons[id] = img
	}
	return icons
}()
