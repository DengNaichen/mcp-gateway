import subprocess,os,json,time,pathlib,tempfile,sys,urllib.request,urllib.error
ROOT=pathlib.Path(__file__).resolve().parents[4]
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
 # Optional representative matrix data, isolated in this run's disposable profile.
 if '--repo-matrix' in sys.argv:
  base=call('/status')['ui_url']
  def put(path,data):
   request=urllib.request.Request(base+'/api'+path,data=json.dumps(data).encode(),method='PUT',headers={'Content-Type':'application/json'})
   try:
    with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
   except urllib.error.HTTPError as error:raise RuntimeError(error.read().decode()) from error
  for sid,name in [('github','GitHub'),('linear','Linear'),('custom','Internal Platform With A Very Long Service Name')]:
   put('/servers',{'id':sid,'name':name,'kind':'http','enabled':True,'endpoint':'http://127.0.0.1:1/mcp'})
  for name in ['read','write']:
   put('/tools',{'id':'github.'+name,'server_id':'github','name':name,'description':'Disposable matrix fixture','enabled':True,'read_only':name=='read','actors':['developer'],'input_schema':{'type':'object'},'method':'GET','path':'/'})
  for index,name in enumerate(['mcp-gateway','billing-platform-with-a-very-long-repository-name','docs']):
   repo=put('/scopes/repositories',{'name':name,'root_paths':[str(root/name)],'remote_hint':'https://github.com/example/'+name+'.git'})
   if index==0:put('/scopes/rules',{'repo_key':repo['id'],'agent_key':'*','grants':[{'server_id':'github','access':'rw','tool_ids':['github.read','github.write']},{'server_id':'linear','access':'ro'}]})
   if index==2:put('/scopes/rules',{'repo_key':repo['id'],'agent_key':'*','grants':[]})
  put('/scopes/rules',{'repo_key':'*','agent_key':'*','grants':[{'server_id':'github','access':'ro'}]})
  # The app polls bootstrap every four seconds; allow that real refresh path.
  time.sleep(5)
 for mode in ['light','dark']:
  call('/theme',{'source':mode})
  for view in ['overview','services','market','catalog','repos','playground','logs','settings','clients']:
   call('/view',{'view':view});time.sleep(1 if view=='repos' else .15)
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
