// gateway-plugin-release creates the signed manifest accompanying a plugin
// release Hook. It runs in the release workflow, not in the desktop app.
package main

import (
	"flag"
	"fmt"
	"os"

	"gatewaylab/internal/clientplugin"
)

func run() error {
	version := flag.String("version", "", "Plugin release version")
	platform := flag.String("platform", "", "Target platform (darwin-arm64 or darwin-amd64)")
	hookPath := flag.String("hook", "", "Compiled Hook path")
	flag.Parse()

	if *platform != "darwin-arm64" && *platform != "darwin-amd64" {
		return fmt.Errorf("unsupported plugin platform %q", *platform)
	}
	hook, err := os.ReadFile(*hookPath) //nolint:gosec // G304: explicit build input path
	if err != nil {
		return err
	}
	manifest, err := clientplugin.BuildRelease(*version, *platform, hook, os.Getenv("MYGO_UPDATER_PRIVATE_KEY"))
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(manifest)
	return err
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
