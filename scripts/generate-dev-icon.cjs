// NODE_PATH=<icon-tools>/node_modules node scripts/generate-dev-icon.cjs
// Uses the same @resvg/resvg-js renderer as generate-icons.cjs.
const fs = require('node:fs');
const path = require('node:path');
const { Resvg } = require('@resvg/resvg-js');
const assets = path.join(__dirname, '../cmd/gateway-desktop/assets');
const svg = fs.readFileSync(path.join(assets, 'mcp.svg'), 'utf8').replace('</svg>',
 '<rect x="84" y="132" width="92" height="42" rx="10" fill="#e8681c"/><text x="130" y="161" text-anchor="middle" font-family="Arial" font-size="28" font-weight="bold" fill="white">DEV</text></svg>');
fs.writeFileSync(path.join(assets,'icon-dev.svg'), svg);
fs.writeFileSync(path.join(assets,'icon-dev.png'),new Resvg(svg,{fitTo:{mode:'width',value:1024}}).render().asPng());
