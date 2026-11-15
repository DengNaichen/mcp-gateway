import { readFileSync } from 'node:fs';
import { execFileSync, spawnSync } from 'node:child_process';
import { pathToFileURL, fileURLToPath } from 'node:url';

export function assertPublishable({version, head, tagCommit, dirty, release}) {
 if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw Error('Invalid release version');
 if (dirty) throw Error('Release upload requires a clean working tree');
 if (!tagCommit || head !== tagCommit) throw Error(`Release upload requires HEAD at tag v${version}`);
 if (release && release.draft !== true) throw Error(`Release v${version} is already published; use a new version`);
}
export function checkRelease() {
 const cwd=fileURLToPath(new URL('../',import.meta.url));
 const {version}=JSON.parse(readFileSync(new URL('../internal/buildinfo/profiles.json',import.meta.url)));
 const git=(...args)=>execFileSync('git',args,{cwd,encoding:'utf8'}).trim();
 const head=git('rev-parse','HEAD');
 let tagCommit=''; try {tagCommit=git('rev-parse',`refs/tags/v${version}^{commit}`)} catch {}
 // Validate the local state before contacting GitHub.
 const input={version,head,tagCommit,dirty:!!git('status','--porcelain')};
 assertPublishable(input);
 const repo=process.env.GITHUB_REPOSITORY || execFileSync('gh',['repo','view','--json','nameWithOwner','--jq','.nameWithOwner'],{cwd,encoding:'utf8'}).trim();
 const response=spawnSync('gh',['api',`repos/${repo}/releases/tags/v${version}`],{cwd,encoding:'utf8'});
 if(response.error)throw response.error;
 let payload;try{payload=JSON.parse(response.stdout)}catch{throw Error('Unable to inspect existing release; upload stopped')}
 if(response.status!==0 && payload.status!=='404' && payload.status!==404)throw Error('Unable to inspect existing release; upload stopped');
 const release=response.status===0?payload:undefined;
 assertPublishable({...input,release});
 return {version,tag:`v${version}`,repository:repo};
}
if(process.argv[1] && import.meta.url===pathToFileURL(process.argv[1]).href) console.log(JSON.stringify(checkRelease()));
