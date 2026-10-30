import { spawn } from "node:child_process";
import { Type } from "@earendil-works/pi-ai";

// No host read/write/shell tools are exposed. Only Docker's stdin is fed model data.
const container = process.env.GATEWAY_SANDBOX_CONTAINER;
const hostJob = process.env.GATEWAY_SANDBOX_JOB;
const fileProgram = String.raw`
const fs = require('node:fs'), path = require('node:path');
let input = ''; process.stdin.on('data', c => input += c);
process.stdin.on('end', () => {
  try {
    const {op, p} = JSON.parse(input);
    const file = path.resolve('/job', p.path);
    if (!file.startsWith('/job/')) throw Error('Path must be inside /job');
    // Symlinks are resolved in the isolated container; no host tree is mounted.
    if (op === 'read') {
      if (fs.statSync(file).size > 2*1024*1024) throw Error('File exceeds 2 MB; use bash to inspect selected content');
      const lines = fs.readFileSync(file,'utf8').split('\n');
      console.log(lines.slice(Math.max(0,(p.offset||1)-1), Math.max(0,(p.offset||1)-1)+(p.limit||2000)).join('\n'));
    } else if (op === 'write') {
      fs.mkdirSync(path.dirname(file),{recursive:true}); fs.writeFileSync(file,p.content); console.log('Written '+p.path);
    } else {
      const old = fs.readFileSync(file,'utf8');
      if (!p.oldText || old.split(p.oldText).length !== 2) throw Error('oldText must match exactly once');
      fs.writeFileSync(file,old.replace(p.oldText,()=>p.newText)); console.log('Edited '+p.path);
    }
  } catch(e) { console.error(e.message); process.exitCode=1; }
});`;

function execute(op: string, params: any, signal?: AbortSignal): Promise<any> {
  if (!container || !hostJob) throw Error("Missing gateway sandbox configuration");
  const p = { ...params };
  if (typeof p.path === "string" && p.path.startsWith(hostJob + "/")) p.path = "/job/" + p.path.slice(hostJob.length + 1);
  const seconds = Math.max(1, Math.min(120, Number(p.timeout) || 60));
  const command = op === "bash"
    ? ["timeout", "--kill-after=2", String(seconds), "bash", "-lc", p.command]
    : ["node", "-e", fileProgram];
  return new Promise((resolve, reject) => {
    const child = spawn("docker", ["exec", "-i", "--workdir", "/job", container, ...command], { stdio: ["pipe", "pipe", "pipe"] });
    let text = "", size = 0, killed = false;
    const append = (chunk: Buffer) => { const left = 64*1024-size; if (left > 0) { const part=chunk.subarray(0,left); text+=part.toString(); size+=part.length; } };
    child.stdout.on("data",append); child.stderr.on("data",append);
    const abort = () => { killed=true; child.kill("SIGKILL"); };
    const timer=setTimeout(abort,(seconds+5)*1000);
    signal?.addEventListener("abort",abort,{once:true});
    const cleanup=()=>{clearTimeout(timer);signal?.removeEventListener("abort",abort);};
    child.on("error",e=>{cleanup();reject(e);});
    child.on("close",code=>{cleanup();resolve({content:[{type:"text",text:text || (code===0?"Done":"Sandbox command failed")}],details:{exitCode:code,truncated:size>=64*1024},isError:killed||code!==0});});
    child.stdin.on("error",()=>{});
    child.stdin.end(op === "bash" ? undefined : JSON.stringify({op,p}));
    if(signal?.aborted) abort();
  });
}

export default function(pi: any) {
  const schemas: Record<string, any> = {
    read: Type.Object({path:Type.String(),offset:Type.Optional(Type.Number()),limit:Type.Optional(Type.Number())}),
    write: Type.Object({path:Type.String(),content:Type.String()}),
    edit: Type.Object({path:Type.String(),oldText:Type.String(),newText:Type.String()}),
    bash: Type.Object({command:Type.String(),timeout:Type.Optional(Type.Number())}),
  };
  for (const [name,parameters] of Object.entries(schemas)) {
    pi.registerTool({name,label:name+" (sandbox)",description:`${name} inside the isolated maintenance container. Work in /job. No network or host filesystem access. Use only Node built-in modules.`,parameters,
      execute: (_id: string, params: any, signal?: AbortSignal)=>execute(name,params,signal)});
  }
}
