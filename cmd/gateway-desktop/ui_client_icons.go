package main

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/i18n"
)

//go:embed assets/clients/*.svg
var clientIconFiles embed.FS

var clientIcons = func() map[string]*ui.SVG {
	icons := make(map[string]*ui.SVG)
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi", "antigravity"} {
		raw, err := clientIconFiles.ReadFile("assets/clients/" + id + ".svg")
		if err != nil {
			panic(err)
		}
		icons[id] = ui.MustParseSVG(raw)
	}
	return icons
}()

var clientPathArrow = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M7 17 17 7M7 7h10v10"/></svg>`))

// nearestConfigLocation does not create a file or directory just to reveal it.
func nearestConfigLocation(path string) (string, bool, error) {
	target, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	for {
		info, err := os.Stat(target)
		if err == nil {
			return target, !info.IsDir(), nil
		}
		if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("%s: %w", i18n.T("app.client_config.open_failed"), err)
		}
		parent := filepath.Dir(target)
		if parent == target {
			return "", false, errors.New(i18n.T("app.client_config.not_found", path))
		}
		target = parent
	}
}
func revealClientConfig(path string) error {
	target, file, err := nearestConfigLocation(path)
	if err != nil {
		return err
	}
	if file {
		mygo.Shell.ShowItemInFolder(target)
		return nil
	}
	return mygo.Shell.OpenPath(target)
}
