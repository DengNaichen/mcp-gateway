import subprocess,os,json,time,pathlib,tempfile
ROOT=pathlib.Path(__file__).resolve().parents[1]
os.chdir(ROOT)
root=pathlib.Path(tempfile.mkdtemp(prefix='gateway-theme-'));sock=str(root/'control.sock')
env=dict(os.environ,GATEWAY_CLIENT_CONFIG_HOME=str(root/'clients'),GATEWAY_DESKTOP_CONTROL=sock)
for key in ['GATEWAY_AUTH_FILE','GATEWAY_PUBLIC_URL','GATEWAY_DEV_PUBLIC_URL']:env.pop(key,None)
app=next((ROOT/'build/dev').glob('*/MCP Gateway Dev.app'))/'Contents/MacOS/MCP Gateway Dev'
p=subprocess.Popen([str(app),'-addr','127.0.0.1:0','-data',str(root/'data')],env=env,stdout=open(root/'runtime.log','w'),stderr=subprocess.STDOUT)
def call(path,data=None):
 a=['curl','-fsS','--max-time','3','--unix-socket',sock,'-H','Content-Type: application/json']
 if data is not None:a+=['-d',json.dumps(data)]
 return json.loads(subprocess.check_output(a+['http://localhost'+path],stderr=subprocess.DEVNULL))
try:
 for _ in range(150):
  if p.poll() is not None:raise RuntimeError('app exited')
  try:
   if call('/status').get('window_exists'):break
  except Exception:pass
  time.sleep(.1)
 else:raise RuntimeError('app did not start')
 for mode in ['light','dark']:
  call('/theme',{'source':mode})
  for view in ['overview','services','market','catalog','repos','playground','logs','settings','clients']:
   call('/view',{'view':view});time.sleep(.15)
   subprocess.run(['curl','-fsS','--unix-socket',sock,'http://localhost/screenshot','-o',str(root/(mode+'-'+view+'.png'))],check=True,stdout=subprocess.DEVNULL)
  assert call('/status')['dark']==(mode=='dark')
 call('/theme',{'source':'light'});assert not call('/status')['dark']
 call('/theme',{'source':'system'})
 (root/'metadata.json').write_text(json.dumps({'git_head':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'git_dirty':bool(subprocess.check_output(['git','status','--porcelain'],text=True).strip()),'workspace':str(ROOT)}))
 print('PASS: native light/dark transitions and nine pages per theme');print('Artifacts:',root)
finally:
 try:call('/tray/quit',{})
 except Exception:p.terminate()
 p.wait(timeout=5)
