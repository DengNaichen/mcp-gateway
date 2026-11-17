import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkRelease } from './release-policy.mjs';
const root = fileURLToPath(new URL('../', import.meta.url));
const profiles = JSON.parse(readFileSync(new URL('../internal/buildinfo/profiles.json', import.meta.url), 'utf8'));

const [action = 'package', channel = 'dev', ...args] = process.argv.slice(2);
if (!['dev', 'stable'].includes(channel) || !['package', 'run', 'binary'].includes(action)) throw Error('Usage: build-channel.mjs package|run|binary dev|stable [arguments]');
const upload = args.some(arg => /^--?upload(?:=(?:1|t|true))?$/i.test(arg));
if (channel === 'dev' && upload) throw Error('Dev builds cannot upload releases');
if (upload) checkRelease();
let revision = 'local';
try {
 revision = execFileSync('git', ['rev-parse','--short=8','HEAD'], {cwd:root,encoding:'utf8'}).trim();
 if (execFileSync('git',['status','--porcelain'],{cwd:root,encoding:'utf8'}).trim()) revision += '.dirty';
} catch { /* Source archives have no Git metadata. */ }
const ldflags = `-X gatewaylab/internal/buildinfo.channel=${channel} -X gatewaylab/internal/buildinfo.revision=${revision}`;
const env = {...process.env, CGO_ENABLED:'0', GATEWAY_BUILD_CHANNEL:channel, MYGO_LDFLAGS:ldflags};
if (channel === 'dev') { delete env.MYGO_UPDATER_PRIVATE_KEY; delete env.MACOS_SIGNING_IDENTITY; delete env.MACOS_NOTARY_PROFILE; delete env.NOTARY_KEYCHAIN; }

function parsePlatforms(cliArgs) {
 for (let i = 0; i < cliArgs.length; i++) {
  if (cliArgs[i] === '-platform' && cliArgs[i + 1]) return cliArgs[i + 1].split(',').map(s => s.trim()).filter(Boolean);
  if (cliArgs[i].startsWith('-platform=')) return cliArgs[i].slice('-platform='.length).split(',').map(s => s.trim()).filter(Boolean);
 }
 return [`${process.platform === 'darwin' ? 'darwin' : process.platform}/${process.arch === 'x64' ? 'amd64' : process.arch}`];
}

function buildBundledHelpers() {
 const slug = profiles[channel].slug;
 const targets = parsePlatforms(args);
 const builtDirs = [];
 for (const target of targets) {
  const [goos, goarch] = target.split('/');
  if (!goos || !goarch) continue;
  const dirs = goarch === 'universal' && goos === 'darwin' ? [['darwin', 'arm64'], ['darwin', 'amd64']] : [[goos, goarch]];
  for (const [osName, archName] of dirs) {
   const outDir = path.join(root, 'resources', `${osName}-${archName}`);
   mkdirSync(outDir, { recursive: true });
   const ext = osName === 'windows' ? '.exe' : '';
   for (const [pkg, binName] of [['./cmd/gateway-hook', `${slug}-hook${ext}`], ['./cmd/gatewayctl', `${slug}ctl${ext}`]]) {
    const res = spawnSync('go', ['build', '-ldflags', ldflags, '-o', path.join(outDir, binName), pkg], {
     cwd: root,
     env: { ...env, GOOS: osName, GOARCH: archName },
     stdio: 'inherit',
    });
    if (res.error) throw res.error;
    if (res.status !== 0) process.exit(res.status ?? 1);
   }
   builtDirs.push(outDir);
  }
 }
 return builtDirs;
}

let command;
if (action === 'package') command=['tool','mygo','build',...args];
else if (action === 'run') command=['run','-ldflags',ldflags,'./cmd/gateway-desktop',...args];
else command=['build','-ldflags',ldflags,'-o',`bin/${channel}/gateway-desktop`,...args,'./cmd/gateway-desktop'];

let exitCode = 1;
try {
 if (action === 'package') buildBundledHelpers();
 const result=spawnSync('go',command,{cwd:root,env,stdio:'inherit'});
 if(result.error)throw result.error;
 exitCode = result.status ?? 1;
} finally {
 if (action === 'package') rmSync(path.join(root, 'resources'), { recursive: true, force: true });
}
process.exit(exitCode);
