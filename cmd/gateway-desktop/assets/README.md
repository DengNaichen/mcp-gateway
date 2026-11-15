# Icon source

`mcp.svg` is the unchanged official MCP favicon:
https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/favicon.svg

Upstream Git blob: `a280d7fdb5bd14d93d1289838f0b23850f3f8d01`.

Used temporarily for this personal app. The app name and branding will be revisited before public release.

Run `scripts/generate-icons.cjs` from the repository root to regenerate `icon.png`, `tray.png`, and `web/public/favicon.svg`; dependency setup is documented at the top of that script. The tray variant omits the background for macOS template rendering; the official paths remain unchanged.

`icon-dev.svg` / `icon-dev.png` add a visible orange DEV badge for the development bundle. Regenerate only these files with `NODE_PATH=<icon-tools>/node_modules node scripts/generate-dev-icon.cjs`; the Stable icon is not modified.
