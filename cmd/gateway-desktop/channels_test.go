package main

import (
	"context"
	"encoding/json"
	"gatewaylab/internal/buildinfo"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeRejectsForeignInstances(t *testing.T) {
	root := t.TempDir()
	p := buildinfo.Current()
	base := map[string]any{"status": "ok", "version": p.Version, "channel": p.Channel, "application": p.Identifier, "instance_id": "test-instance", "data_identity": buildinfo.DataIdentity(root)}
	cases := []struct {
		name, key string
		value     any
	}{{"channel", "channel", "other"}, {"application", "application", "other.app"}, {"data", "data_identity", "different"}, {"version", "version", "different"}, {"missing-instance", "instance_id", ""}, {"legacy", "channel", nil}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			for k, v := range base {
				payload[k] = v
			}
			payload[tc.key] = tc.value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/auth/me" {
					json.NewEncoder(w).Encode(map[string]any{"shared": false})
					return
				}
				json.NewEncoder(w).Encode(payload)
			}))
			defer srv.Close()
			if probeGatewayHealth(context.Background(), srv.URL, root) {
				t.Fatal("attached to foreign instance")
			}
		})
	}
}
func TestUpdateChannel(t *testing.T) {
	if buildinfo.Current().Channel == "stable" {
		return
	} // Stable menu needs the live native updater plugin.
	item := channelUpdateMenuItem()
	if buildinfo.Current().Channel == "dev" && !item.Disabled {
		t.Fatal("Dev updater enabled")
	}
}

func TestDevDoesNotInheritStableDataOverride(t *testing.T) {
	if buildinfo.Current().Channel != "dev" {
		return
	}
	production := t.TempDir()
	t.Setenv("GATEWAY_DATA_DIR", production)
	t.Setenv("GATEWAY_DEV_DATA_DIR", "")
	got, err := resolveDataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if got == production {
		t.Fatal("Dev inherited the Stable data directory")
	}
}
