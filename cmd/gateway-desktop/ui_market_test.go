package main

import (
	"testing"

	"gatewaylab/internal/apitypes"
)

func TestBuildMarketViewModel(t *testing.T) {
	state := &apitypes.Bootstrap{
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "github", Demo: false},
				{ID: "demo", Demo: true},
			},
		},
	}
	items := []apitypes.MarketServer{
		{ID: "m-gh", ServerID: "github", Title: "GitHub", Transport: "http", Featured: true},
		{ID: "m-pg", ServerID: "pg", Title: "Postgres", Transport: "stdio", Featured: true},
		{ID: "m-reg", ServerID: "demo", Title: "Remote Demo", Transport: "http", Featured: false},
	}

	vmAll := buildMarketViewModel(state, &marketPageState{filter: "all", items: items})
	if vmAll.installedCount != 1 || len(vmAll.featuredFiltered) != 2 || len(vmAll.registryFiltered) != 1 {
		t.Fatalf("unexpected all vm: %+v", vmAll)
	}

	vmInstalled := buildMarketViewModel(state, &marketPageState{filter: "installed", items: items})
	if len(vmInstalled.featuredFiltered) != 1 || vmInstalled.featuredFiltered[0].ID != "m-gh" || len(vmInstalled.registryFiltered) != 0 {
		t.Fatalf("unexpected installed vm: %+v", vmInstalled)
	}

	vmQuery := buildMarketViewModel(state, &marketPageState{filter: "stdio", query: "postgres", items: items})
	if len(vmQuery.featuredFiltered) != 1 || vmQuery.featuredFiltered[0].ID != "m-pg" {
		t.Fatalf("unexpected query vm: %+v", vmQuery)
	}
}
