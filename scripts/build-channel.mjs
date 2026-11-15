import { execFileSync, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { checkRelease } from './release-policy.mjs';
const root = fileURLToPath(new URL('../', import.meta.url));

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
let command;
if (action === 'package') command=['tool','mygo','build',...args];
else if (action === 'run') command=['run','-ldflags',ldflags,'./cmd/gateway-desktop',...args];
else command=['build','-ldflags',ldflags,'-o',`bin/${channel}/gateway-desktop`,...args,'./cmd/gateway-desktop'];
const result=spawnSync('go',command,{cwd:root,env,stdio:'inherit'});
if(result.error)throw result.error;
process.exit(result.status ?? 1);
