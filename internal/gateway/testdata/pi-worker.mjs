#!/usr/bin/env node
// Deterministic RPC worker used by lifecycle tests. No model or real service calls.
import fs from 'node:fs';
import readline from 'node:readline';
const send = value => process.stdout.write(JSON.stringify(value) + '\n');
const rl = readline.createInterface({ input: process.stdin });
rl.on('line', line => {
 const command = JSON.parse(line);
 if (command.type !== 'prompt') return;
 const input = JSON.parse(fs.readFileSync('input.json', 'utf8'));
 const source = JSON.parse(input.references[0].content);
 if(input.server.kind==='mcp'){
  const native=JSON.parse(input.references.find(r=>r.name==='native-tools.json').content);
  const tools=native.map(t=>({id:input.server.id+'.'+t.name,server_id:input.server.id,name:t.name,description:t.description,tags:['native'],input_schema:t.inputSchema,read_only:false}));
  fs.writeFileSync('output/manifest.json',JSON.stringify({summary:'同步原生工具',tools}));
  send({type:'response',id:command.id,success:true,data:{disposition:'started'}});
  send({type:'message_end',message:{role:'assistant',stopReason:'stop'}});
  send({type:'agent_settled',aborted:false});return;
 }
 send({type:'response', id:command.id, success:true, data:{disposition:'started'}});
 if (source.mode === 'hang') {setInterval(()=>{},1000);return;}
 if (source.mode === 'crash') process.exit(4);
 if (source.mode === 'error') {
  send({type:'message_end',message:{role:'assistant',stopReason:'error'}});
  send({type:'agent_settled',aborted:false});return;
 }
 const tool = {id: input.server.id+'.logs', server_id:input.server.id, name:'logs', description:'Search service error logs '+source.version, tags:['logs','errors'],input_schema:{type:'object',properties:{service:{type:'string'}},required:['service'],additionalProperties:false},read_only:true,example:{service:'billing'},method:'GET',path:'/logs/'+source.version};
 if (source.mode === 'adapter') {
  tool.adapter='adapter.mjs';delete tool.method;delete tool.path;
  fs.writeFileSync('output/adapter.mjs', `import fs from 'node:fs';
const input=JSON.parse(fs.readFileSync(0,'utf8'));
const response=await fetch(input.endpoint+'/logs/'+encodeURIComponent(input.arguments.service),{headers:input.headers});
if(!response.ok)throw new Error('upstream failed');
console.log(JSON.stringify(await response.json()));`);
  fs.writeFileSync('output/verify.mjs', `import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import http from 'node:http';
const server=http.createServer((req,res)=>{ if(req.url!='/logs/billing'){res.writeHead(400);res.end();return;}res.setHeader('Content-Type','application/json');res.end(JSON.stringify({ok:true}));});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
try{const child=spawn(process.execPath,['adapter.mjs']);let out='';child.stdout.on('data',b=>out+=b);child.stdin.end(JSON.stringify({endpoint:'http://127.0.0.1:'+server.address().port,arguments:{service:'billing'},headers:{}}));const code=await new Promise(r=>child.on('exit',r));assert.equal(code,0);assert.deepEqual(JSON.parse(out),{ok:true});}finally{server.close();}`);
 }
 if (source.mode === 'invalid') tool.input_schema = {type:'nonsense'};
 const tools=source.mode === 'empty'?[]:[tool];
 fs.writeFileSync('output/manifest.json',JSON.stringify({summary:'维护版本 '+source.version,tools}));
 if(source.mode==='symlink-output') {fs.renameSync('output','moved-output');fs.symlinkSync('moved-output','output');}
 send({type:'tool_execution_start',toolName:'write'});
 send({type:'agent_end'}); // This is deliberately NOT the completion signal.
 send({type:'message_end',message:{role:'assistant',stopReason:'stop'}});
 send({type:'agent_settled',aborted:false});
});
