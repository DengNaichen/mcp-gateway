package api

import (
	"context"
	"gatewaylab/internal/maintenance/sandbox"
	"os"
	"strings"
	"testing"
)

func TestDockerExecutionIsolation(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_DOCKER") != "1" {
		t.Skip("set GATEWAY_TEST_DOCKER=1 to exercise Docker isolation")
	}
	g, ref := testGatewayOptions(t, "", &sandbox.Policy{Mode: "docker", Image: "node@sha256:4b150b48b4116e2a22f2c39cc27025fbf4f153e2078fd3b14d159fdb5c7279da"})
	putSource(t, ref, "v1", "adapter")
	setLogsAcceptance(t, g, "v1", "adapter")
	g.StartMaintenance(context.Background())
	run := approveRun(t, g.Maintenance(), enqueueTest(t, g.Maintenance(), true).ID)
	if !run.Acceptance.Passed {
		t.Fatal("container adapter acceptance failed")
	}
}

func TestOutputDirectorySymlinkRejected(t *testing.T) {
	g, reference := testGateway(t)
	putSource(t, reference, "v1", "symlink-output")
	g.StartMaintenance(context.Background())
	run := waitRun(t, g.Maintenance(), enqueueTest(t, g.Maintenance(), true).ID, "failed")
	if !strings.Contains(run.Error, "符号链接") {
		t.Fatalf("wrong rejection: %s", run.Error)
	}
}
