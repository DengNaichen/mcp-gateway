import test from 'node:test';
import assert from 'node:assert/strict';
import { assertPublishable } from './release-policy.mjs';
import { spawnSync } from 'node:child_process';
const clean={version:'0.1.0',head:'abc',tagCommit:'abc',dirty:false};
test('new or draft release at exact clean tag is allowed',()=>{
 assert.doesNotThrow(()=>assertPublishable(clean));
 assert.doesNotThrow(()=>assertPublishable({...clean,release:{draft:true}}));
});
test('published releases and wrong source cannot be uploaded',()=>{
 for(const change of [{release:{draft:false}},{head:'other'},{tagCommit:''},{dirty:true},{version:'bad'}]) assert.throws(()=>assertPublishable({...clean,...change}));
});
test('ordinary package configuration is Dev and has no update source',()=>{
 const env={...process.env};delete env.GATEWAY_BUILD_CHANNEL;delete env.MYGO_LDFLAGS;
 const result=spawnSync(process.execPath,['--input-type=module','-e','import c from "./mygo.config.ts"; console.log(JSON.stringify(c))'],{env,encoding:'utf8'});
 assert.equal(result.status,0,result.stderr);const c=JSON.parse(result.stdout);assert.equal(c.name,'MCP Gateway Dev');assert.equal(c.identifier,'NaichengDeng.mcp-gateway.dev');assert.equal(c.updates,undefined);assert.equal(c.out,'build/dev');assert.equal(c.macos.signingIdentity,'-');
});
test('Stable packaging without matching runtime flags fails closed',()=>{
 const result=spawnSync(process.execPath,['--input-type=module','-e','import "./mygo.config.ts"'],{env:{...process.env,GATEWAY_BUILD_CHANNEL:'stable',MYGO_LDFLAGS:''},encoding:'utf8'});assert.notEqual(result.status,0);assert.match(result.stderr,/matching runtime identity/);
});
test('Dev cannot enter upload path',()=>{for (const flag of ['-upload','--upload','-upload=true','--upload=true','-upload=1','-upload=T']) { const result=spawnSync(process.execPath,['scripts/build-channel.mjs','package','dev',flag],{encoding:'utf8'});assert.notEqual(result.status,0);assert.match(result.stderr,/cannot upload/); }});
