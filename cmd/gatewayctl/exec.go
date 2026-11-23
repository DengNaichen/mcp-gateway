package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/workspace"
)

const approvalExitCode = 75

var errApprovalRequired = errors.New("approval required")

func runExec(args []string, opts gatewayCommandOptions, stdout io.Writer) error {
	fs := flag.NewFlagSet("gatewayctl exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	service := fs.String("service", "gh", "Controlled GitHub CLI source ID")
	skill := fs.String("skill", "", "Declare a skill ID")
	approval := fs.String("approval", "", "Retry with this approval ID")
	scopeMode := fs.String("scope", "auto", "Sign a scope automatically or turn signing off")
	agent := fs.String("agent", "gatewayctl", "Agent identity for scope signing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: gatewayctl exec [-service gh] [-skill id] [-approval id] [-scope auto|off] [-agent gatewayctl] [--] <gh arguments>")
	}
	if !workspace.IsKnownAgent(*agent) {
		return fmt.Errorf("unknown scope agent %q", *agent)
	}
	var signScope func([]byte) (string, error)
	switch *scopeMode {
	case "auto":
		key, err := workspace.LoadScopeKey("")
		if err != nil {
			return fmt.Errorf("scope auto could not read scope.key: %w (use -scope off for an explicitly unsigned request)", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine scope working directory: %w", err)
		}
		signScope = func(body []byte) (string, error) {
			hash, _, _, err := workspace.CanonicalArgsHashJSON(body, workspace.CurrentContextField())
			if err != nil {
				return "", err
			}
			return workspace.SignToken(key, workspace.Claims{Agent: *agent, Cwd: cwd, Op: "cli_exec", ArgsHash: hash})
		}
	case "off":
	default:
		return errors.New("exec -scope must be auto or off")
	}

	client := apiclient.New(opts.Endpoint, apiclient.WithToken(os.Getenv("GATEWAY_TOKEN")))
	in := apitypes.CLIExecRequest{ServiceID: *service, SkillID: *skill, ApprovalID: *approval, Actor: opts.Actor, Argv: fs.Args()}
	out, err := client.CLIExec(context.Background(), in, signScope)
	if err != nil {
		return err
	}
	if out.Approval == nil || out.Status != "approval_required" && out.Status != "approval_pending" {
		return writePrettyValueJSON(stdout, out)
	}

	// The scope and command arguments stay with the caller. The challenge
	// prints a retry instruction without echoing a potentially private body.
	retry := fmt.Sprintf("Rerun the same command with -approval=%s immediately after exec, replacing any existing -approval.", out.Approval.ApprovalID)
	err = writePrettyValueJSON(stdout, struct {
		apitypes.InvokeResult
		ApprovalID   string `json:"approval_id"`
		RetryCommand string `json:"retry_command"`
	}{InvokeResult: out, ApprovalID: out.Approval.ApprovalID, RetryCommand: retry})
	if err != nil {
		return err
	}
	return errApprovalRequired
}
