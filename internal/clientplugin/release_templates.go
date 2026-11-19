package clientplugin

import (
	"encoding/json"
	"path"
	"strings"

	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/workspace"
)

const (
	templateEndpoint     = "https://mcp-gateway-plugin-template.invalid/mcp"
	templateHook         = "__MCP_GATEWAY_PLUGIN_HOOK__"
	templateData         = "__MCP_GATEWAY_PLUGIN_DATA__"
	templateInstallation = "__MCP_GATEWAY_PLUGIN_INSTALLATION__"
)

// releaseTemplates uses the normal host renderers with reserved values for
// machine-local settings. The files include the host-specific Hook matcher,
// so changing it can ship in a plugin release independently of the app.
func (m *Manager) releaseTemplates() (map[string][]renderedFile, error) {
	const url, hook, data = templateEndpoint, templateHook, templateData
	sources := map[string]func() (renderedBundle, error){
		ClientClaude:      func() (renderedBundle, error) { return m.renderClaudeMarketplace(url, hook, data) },
		ClientCodex:       func() (renderedBundle, error) { return m.renderCodexMarketplace(url, hook, data, templateInstallation) },
		ClientCursor:      func() (renderedBundle, error) { return m.renderCursorPlugin(url, hook, data) },
		ClientOpenCode:    func() (renderedBundle, error) { return m.renderOpenCodePlugin(url, hook, data) },
		ClientPi:          func() (renderedBundle, error) { return m.renderPiPackage(url, hook, data) },
		ClientAntigravity: func() (renderedBundle, error) { return m.renderAntigravityPlugin(url, hook, data) },
	}
	templates := make(map[string][]renderedFile, len(sources))
	for id, render := range sources {
		bundle, err := render()
		if err != nil {
			return nil, err
		}
		templates[id] = bundle.RootFiles
	}
	return templates, nil
}

type releaseRender struct {
	client       string
	endpoint     string
	hook         string
	data         string
	installation string
}

func renderReleasedBundle(release *releaseManifest, opts releaseRender) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(opts.endpoint, opts.client)
	jsonQuote := func(value any) string {
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	templateAgentURL := clientconfig.ClientEndpoint(templateEndpoint, opts.client)
	endpoint := strings.NewReplacer(jsonQuote(templateAgentURL), strings.TrimSpace(string(encodePrettyJSON(agentURL))))
	var extraArgs []string
	if opts.data != "" {
		extraArgs = []string{"--data", opts.data}
	}
	script := strings.NewReplacer(
		jsonQuote(templateAgentURL), jsonQuote(agentURL),
		jsonQuote(templateHook), jsonQuote(opts.hook),
		jsonQuote([]string{"--data", templateData}), jsonQuote(extraArgs),
	)
	shell := strings.NewReplacer(
		templateHook, workspace.ShellQuote(opts.hook),
		templateData, workspace.ShellQuote(opts.data),
		" --installation "+templateInstallation, installationArg(opts.installation),
	)

	files := make([]renderedFile, 0, len(release.Templates[opts.client]))
	for _, file := range release.Templates[opts.client] {
		switch {
		case path.Base(file.RelPath) == "run-gateway-hook":
			file.Data = []byte(shell.Replace(string(file.Data)))
		case path.Ext(file.RelPath) == ".js" || path.Ext(file.RelPath) == ".ts":
			file.Data = []byte(script.Replace(string(file.Data)))
		case path.Ext(file.RelPath) == ".json":
			file.Data = []byte(endpoint.Replace(string(file.Data)))
		}
		files = append(files, file)
	}
	return renderedBundle{
		RootFiles: files, Digest: hashRenderedFiles(files, agentURL, opts.hook, opts.data, release.Version),
		HookBin: opts.hook, Endpoint: agentURL,
	}
}

func installationArg(id string) string {
	if id == "" {
		return ""
	}
	return " --installation " + workspace.ShellQuote(id)
}
