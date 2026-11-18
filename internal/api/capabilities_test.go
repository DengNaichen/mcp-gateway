package api

import (
	"context"
	"strings"
	"testing"

	"gatewaylab/internal/gateway"
)

func TestSummaryDiscoveryAndSchemaAuthorization(t *testing.T) {
	g, svcs, _ := testGateway(t)
	svcs.Maintenance.Start(context.Background())
	approveRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, false).ID)
	result, err := g.Discover(context.Background(), gateway.DiscoverRequest{Intent: "logs"})
	if err != nil {
		t.Fatal(err)
	}
	b := mustJSON(t, discoveryPresentation(result, true))
	if strings.Contains(string(b), "input_schema") {
		t.Fatal("summary discovery contained schema")
	}
	tool, err := g.ToolSchema(context.Background(), "service.logs", "developer")
	if err != nil || tool.InputSchema == nil {
		t.Fatalf("schema retrieval: %+v %v", tool, err)
	}
	if _, err := g.ToolSchema(context.Background(), "service.logs", "observer"); err == nil {
		t.Fatal("schema leaked to unauthorized actor")
	}
}
