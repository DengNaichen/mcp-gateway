package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"gatewaylab/internal/maintenance/sandbox"
)

func TestDockerExecutionIsolation(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_DOCKER") != "1" {
		t.Skip("set GATEWAY_TEST_DOCKER=1 to exercise Docker isolation")
	}
	_, svcs, ref := testGatewayOptions(t, "", &sandbox.Policy{Mode: "docker", Image: "node@sha256:4b150b48b4116e2a22f2c39cc27025fbf4f153e2078fd3b14d159fdb5c7279da"})
	putSource(t, ref, "v1", "adapter")
	setLogsAcceptance(t, svcs.Maintenance, "v1", "adapter")
	svcs.Maintenance.Start(context.Background())
	run := approveRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, true).ID)
	if !run.Acceptance.Passed {
		t.Fatal("container adapter acceptance failed")
	}
}

func TestOutputDirectorySymlinkRejected(t *testing.T) {
	_, svcs, reference := testGateway(t)
	putSource(t, reference, "v1", "symlink-output")
	svcs.Maintenance.Start(context.Background())
	run := waitRun(t, svcs.Maintenance, enqueueTest(t, svcs.Maintenance, true).ID, "failed")
	if !strings.Contains(run.Error, "符号链接") {
		t.Fatalf("wrong rejection: %s", run.Error)
	}
}
