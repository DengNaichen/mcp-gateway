package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// Controlled CLI (Kind = "cli") services. The gateway pins the resolved
// executable by SHA-256 at registration and only reads GH_TOKEN from its own
// credential store (system keychain), never from the host environment.

const (
	serviceAddTabCLI = 4

	ghPresetID      = "gh"
	ghPresetName    = "GitHub CLI"
	ghPresetCommand = "gh"
	ghTokenEnv      = "GH_TOKEN"

	cliDigestPrefixLen = 12
)

func serverIsCLI(s apitypes.Server) bool { return s.Kind == "cli" }

// ghPresetEnv is the only environment shape the gateway accepts for CLI sources.
func ghPresetEnv() map[string]string {
	return map[string]string{ghTokenEnv: "${" + ghTokenEnv + "}"}
}

// cliDigestShort returns the first 12 hex characters of a pinned digest.
func cliDigestShort(sum string) string {
	sum = strings.ToLower(strings.TrimSpace(sum))
	if len(sum) > cliDigestPrefixLen {
		return sum[:cliDigestPrefixLen]
	}
	return sum
}

// cliCommandSummary renders a controlled CLI action ("gh.pr.view") as the
// subcommand it executes ("gh pr view"). Non-CLI actions are returned as-is.
func cliCommandSummary(serviceID, action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	rest, ok := strings.CutPrefix(action, serviceID+".")
	if !ok || serviceID == "" {
		return action
	}
	return "gh " + strings.ReplaceAll(rest, ".", " ")
}

// toolMatchesApproveRule reports whether any enabled Approve rule can match the
// tool regardless of repository, i.e. the call may be held for human review.
func toolMatchesApproveRule(t apitypes.Tool, rules []apitypes.PolicyRule) bool {
	return slices.ContainsFunc(rules, func(r apitypes.PolicyRule) bool {
		if !r.Enabled || r.Effect != "approve" {
			return false
		}
		if r.ServiceID != "*" && r.ServiceID != t.ServerID {
			return false
		}
		if len(r.ToolIDs) > 0 && !slices.Contains(r.ToolIDs, t.ID) {
			return false
		}
		return r.Access == "*" || r.Access == "ro" && t.ReadOnly || r.Access == "rw" && !t.ReadOnly
	})
}

func (s *serviceAddFormState) applyGHPreset() {
	s.tab = serviceAddTabCLI
	s.errMsg = ""
	s.id = ghPresetID
	s.name = ghPresetName
	if strings.TrimSpace(s.cliCommand) == "" {
		s.cliCommand = ghPresetCommand
	}
}

func (s *serviceAddFormState) buildCLIServerPayload() (apitypes.Server, error) {
	id, name := s.resolveIdentity(ghPresetID)
	cmd := strings.TrimSpace(s.cliCommand)
	if cmd == "" {
		return apitypes.Server{}, errors.New(i18n.T("services.err.command_required"))
	}
	sum := strings.ToLower(strings.TrimSpace(s.cliSHA))
	if sum != "" && !isHexDigest(sum) {
		return apitypes.Server{}, errors.New(i18n.T("services.cli.err_sha"))
	}
	return apitypes.Server{
		ID:            id,
		Name:          name,
		Description:   strings.TrimSpace(s.description),
		Kind:          "cli",
		Command:       cmd,
		CommandSHA256: sum,
		Env:           ghPresetEnv(),
		Enabled:       true,
	}, nil
}

func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func renderAddCLIFields(c *ui.Context, add *serviceAddFormState) {
	ui.Text(c, i18n.T("services.cli.preset_note")).FontSize(12).LineHeight(1.45).TextColor(SailInkMuted)
	renderAddCommonIdentityFields(c, add, "cli")
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.cli.command_label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-cli-cmd"), &add.cliCommand).
				Font("monospace").
				Placeholder("gh  /  /opt/homebrew/bin/gh").
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.cli.sha_label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-cli-sha"), &add.cliSHA).
				Font("monospace").
				Placeholder(i18n.T("services.cli.sha_placeholder")).
				FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, ghTokenEnv).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.Text(c, i18n.T("services.cli.token_hint")).FontSize(11.5).TextColor(SailInkMuted)
		})
		SailTextInput(c.Key("add-cli-token"), &add.cliToken).
			Password().
			Placeholder(i18n.T("services.cli.token_placeholder")).
			FillWidth()
	})
}

// submitAddCLIService registers the controlled CLI (pinning its digest), then
// optionally stores GH_TOKEN in the gateway keychain and verifies the source.
func (u *DesktopUI) submitAddCLIService(st *servicesPageState) {
	if u.Client == nil || st.add.busy {
		return
	}
	st.add.errMsg = ""
	payload, err := st.add.buildCLIServerPayload()
	if err != nil {
		st.add.errMsg = err.Error()
		return
	}
	token := st.add.cliToken
	st.add.busy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		saved, err := u.Client.SaveServer(ctx, payload)
		if err != nil {
			return u.handleAddServiceError(st, err)
		}
		msg := i18n.T("services.cli.toast_registered", cliDigestShort(saved.CommandSHA256))
		if token != "" {
			if err := u.Client.SaveServerCredentials(ctx, saved.ID, map[string]string{ghTokenEnv: token}); err != nil {
				_ = u.RefreshSync()
				return u.handleAddServiceError(st, err)
			}
			check, probeErr := u.Client.TestSource(ctx, saved)
			switch {
			case probeErr != nil && apiclient.IsUnauthorized(probeErr):
				u.updateUI(func() { st.add.busy = false })
				return probeErr
			case probeErr != nil:
				msg = i18n.T("ops.credentials-saved-probe-failed", formatAPIError(probeErr))
			case check.Message != "":
				msg = check.Message
			}
		}
		if err := u.RefreshSync(); err != nil && apiclient.IsUnauthorized(err) {
			u.updateUI(func() { st.add.busy = false })
			return err
		}
		u.finishAddService(st, saved.ID, msg)
		return nil
	})
}

// renderServiceConnectionCLIFields shows the pinned executable read-only: the
// gateway rejects any change that does not match the registered digest.
func (u *DesktopUI) renderServiceConnectionCLIFields(c *ui.Context, server apitypes.Server) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		SailKeyValueRow(c, i18n.T("services.cli.command_label"), ServerTargetText(server), true)
		SailKeyValueRow(c, "SHA-256", strings.ToLower(server.CommandSHA256), true)
		SailKeyValueRow(c, i18n.T("services.field.env"), ghTokenEnv+" = ${"+ghTokenEnv+"}", true)
	})
	ui.Text(c, i18n.T("services.cli.pin_note")).FontSize(11.5).LineHeight(1.45).TextColor(SailInkMuted)
}

// renderServiceCLIDigest renders the 12-character digest prefix with a copy
// action that copies the full pinned SHA-256.
func (u *DesktopUI) renderServiceCLIDigest(c *ui.Context, server apitypes.Server) {
	full := strings.ToLower(strings.TrimSpace(server.CommandSHA256))
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, "SHA-256").FontSize(11.5).TextColor(SailInkMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if full == "" {
				ui.Text(c, i18n.T("services.cli.unpinned")).FontSize(12.5).TextColor(SailDangerInk).SingleLine()
				return
			}
			ui.Text(c, cliDigestShort(full)).Font("monospace").FontSize(12.5).TextColor(SailInkStrong).SingleLine().Tooltip(full).Selectable()
			if SailSmallButton(c.Key("detail-copy-sha-"+server.ID), i18n.T("common.copy")).Padding(2, 7).FontSize(11).Clicked() {
				c.WriteClipboard(full)
				u.Notify(i18n.T("services.cli.toast_sha_copied"))
			}
		})
	})
}
