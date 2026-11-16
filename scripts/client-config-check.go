// Run with go run ./scripts/client-config-check.go. Used for explicit isolated
// integration verification of the same configuration manager as the desktop.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gatewaylab/internal/clientconfig"
)

func main() {
	root := flag.String("home", "", "explicit test config home (required)")
	state := flag.String("state", "", "receipt directory (required)")
	client := flag.String("client", "", "client ID")
	action := flag.String("action", "status", "status, write, update, revoke")
	endpoint := flag.String("endpoint", "http://127.0.0.1:18878/mcp", "Gateway MCP URL")
	flag.Parse()
	if *root == "" || *state == "" {
		fmt.Fprintln(os.Stderr, "home and state are required")
		os.Exit(2)
	}
	m := clientconfig.New(*root, *state, func(string) string { return "" })
	var err error
	switch *action {
	case "write":
		err = m.Write(*client, *endpoint)
	case "update":
		err = m.Update(*client, *endpoint)
	case "revoke":
		err = m.Revoke(*client)
	case "status":
	default:
		err = fmt.Errorf("unknown action")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, s := range m.Statuses(*endpoint) {
		if *client == "" || s.ID == *client {
			json.NewEncoder(os.Stdout).Encode(s)
		}
	}
}
