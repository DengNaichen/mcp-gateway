// npm install --prefix /tmp/mcp-gateway-icon-tools --no-audit --no-fund @resvg/resvg-js
// NODE_PATH=/tmp/mcp-gateway-icon-tools/node_modules node scripts/generate-icons.cjs
const { readFileSync, writeFileSync } = require('node:fs');
const { resolve } = require('node:path');
const { Resvg } = require('@resvg/resvg-js');

const root = resolve(__dirname, '..');
const assets = resolve(root, 'cmd/gateway-desktop/assets');
const source = readFileSync(resolve(assets, 'mcp.svg'), 'utf8');
const render = (svg, width) => new Resvg(svg, { fitTo: { mode: 'width', value: width } }).render().asPng();

writeFileSync(resolve(assets, 'icon.png'), render(source, 1024));
// macOS template images use alpha as a silhouette; omit the favicon's tile.
const template = source.replace(/<rect\b[^>]*\/>/, '').replaceAll('stroke="white"', 'stroke="black"');
writeFileSync(resolve(assets, 'tray.png'), render(template, 36));
console.log('Generated desktop icon and template tray icon from mcp.svg.');
