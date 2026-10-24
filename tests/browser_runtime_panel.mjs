import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
const evidence=process.env.FNPACKKIT_RUNTIME_EVIDENCE;
if(evidence)await mkdir(evidence,{recursive:true});
import { pathToFileURL } from 'node:url';
const runtime=process.env.BUILDFLOW_BROWSER_RUNTIME;
if(!runtime)throw new Error('registered BuildFlow browser runtime is required');
const { withBrowser }=await import(pathToFileURL(runtime).href);

const prefix='/app/panel-test';
const root=new URL('../runtime/runtimepanel/web/',import.meta.url);
let running=true;
let paused=false;
let starts=0;
let stops=0;
const controls=[];
const server=createServer(async(req,res)=>{
  const route=new URL(req.url,'http://localhost').pathname;
  res.setHeader('Cache-Control','no-store');
  const snapshot=()=>({schema:1,state:running?'running':paused?'paused':'stopped',paused});
  if(route==='/__fixture_idle' && req.method==='POST'){running=false;paused=false;res.end('ok');return;}
  if(route===prefix+'/_runtime/control') {
    if(req.method==='POST') {
      let body='';for await(const chunk of req)body+=chunk;
      const c=JSON.parse(body);controls.push(c);
      assert.equal(req.headers['x-fnpackkit-control'],'1');
      if(c.action==='stop'||c.action==='restart'){assert.equal(c.confirm,true);running=false;paused=true;stops++;}
      if(c.action==='start'||c.action==='restart'){running=true;paused=false;starts++;}
    }
    res.setHeader('Content-Type','application/json');res.end(JSON.stringify(snapshot()));return;
  }
  if(route===prefix+'/_runtime/manifest') {
    res.setHeader('Content-Type','application/json');
    res.end(JSON.stringify({name:'测试应用',prefix,actions:[{id:'settings',label:'终端显示设置',selector:'#settings-toggle'}]}));return;
  }
  if(route.startsWith(prefix+'/_runtime/')) {
    const file=route.slice((prefix+'/_runtime/').length);
    if(!['bootstrap.js','panel.js','drag.js','panel.css'].includes(file)){res.writeHead(404);res.end();return;}
    res.setHeader('Content-Type',file.endsWith('.css')?'text/css':'text/javascript');
    res.end(await readFile(new URL(file,root)));return;
  }
  res.setHeader('Content-Type','text/html');
  res.end('<!doctype html><html lang="zh-CN"'+(paused?' data-runtime-page':'')+'><head><meta charset="utf-8"><script type="module" src="'+prefix+'/_runtime/bootstrap.js"></script></head><body><h1>测试业务</h1><button id="settings-toggle" onclick="document.getElementById(&quot;settings&quot;).hidden=false">显示设置</button><div id="settings" hidden>终端设置</div></body></html>');
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const url='http://127.0.0.1:'+server.address().port+prefix+'/';
const checks=[];const pageErrors=[];
try {
 await withBrowser({},async browser=>{
  const page=await browser.newPage({viewport:{width:1000,height:760}});
  page.on('pageerror',e=>pageErrors.push(e.message));
  await page.goto(url);
  const toggle=page.getByRole('button',{name:'后端控制',exact:true});
  await toggle.waitFor();
  await toggle.click();await page.getByText('运行中',{exact:true}).waitFor();
  assert.equal(starts,0);checks.push('initial status never starts backend');
  if(evidence)await page.screenshot({path:resolve(evidence,'runtime-panel.png'),animations:'disabled'});
  await page.getByRole('button',{name:'终端显示设置',exact:true}).click();
  await page.locator('#settings').waitFor({state:'visible'});
  checks.push('application extension opens upstream settings');
  await page.evaluate(()=>{
    const previous=document.querySelector('#settings-toggle');
    previous.onclick=()=>{document.body.dataset.detachedClicks='unexpected';};
    const replacement=document.createElement('button');replacement.id='settings-toggle';replacement.textContent='新设置入口';
    replacement.onclick=()=>{document.body.dataset.settingsClicks=String(Number(document.body.dataset.settingsClicks||0)+1);document.querySelector('#settings').hidden=false;};
    previous.replaceWith(replacement);document.querySelector('#settings').hidden=true;
  });
  await page.waitForFunction(()=>{const target=document.querySelector('#settings-toggle');return target.hidden&&target.style.display==='none';});
  await toggle.click();
  const extension=page.getByRole('button',{name:'终端显示设置',exact:true});
  assert.equal(await extension.count(),1);
  await extension.click();await page.locator('#settings').waitFor({state:'visible'});
  assert.equal(await page.locator('body').getAttribute('data-settings-clicks'),'1');
  assert.equal(await page.locator('body').getAttribute('data-detached-clicks'),null);
  checks.push('SPA replacement hides the new entry and targets the current node');
  await toggle.click();
  await page.evaluate(()=>{
    document.querySelector('#settings').hidden=true;
    window.detachedSettingsTarget=document.querySelector('#settings-toggle');window.detachedSettingsTarget.remove();
    document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('[data-action="settings"]').click();
  });
  assert.equal(await extension.isDisabled(),true);
  assert.equal(await toggle.getAttribute('aria-expanded'),'true');
  assert.equal(await page.locator('body').getAttribute('data-settings-clicks'),'1');
  await page.evaluate(()=>{document.body.append(window.detachedSettingsTarget);delete window.detachedSettingsTarget;});
  await page.waitForFunction(()=>{
    const target=document.querySelector('#settings-toggle');
    const action=document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('[data-action="settings"]');
    return target.hidden&&target.style.display==='none'&&!action.disabled;
  });
  assert.equal(await extension.count(),1);await extension.click();
  assert.equal(await page.locator('body').getAttribute('data-settings-clicks'),'2');
  assert.equal(await page.locator('body').getAttribute('data-detached-clicks'),null);
  checks.push('missing extension target stays inactive and recovers after remount');
  await toggle.click();
  await page.getByRole('button',{name:'停止后端',exact:true}).click();
  await page.getByRole('button',{name:'取消',exact:true}).click();
  assert.equal(stops,0);checks.push('cancel preserves backend');
  await page.getByRole('button',{name:'停止后端',exact:true}).click();
  await Promise.all([page.waitForNavigation({waitUntil:'load'}),page.getByRole('button',{name:'确认停止',exact:true}).click()]);
  await page.getByText('已手动停止',{exact:true}).waitFor();
  await page.reload();await page.getByText('已手动停止',{exact:true}).waitFor();
  assert.equal(starts,0);assert.equal(stops,1);checks.push('confirmed stop and reload stay paused');
  await Promise.all([page.waitForNavigation({waitUntil:'load'}),page.getByRole('button',{name:'启动后端',exact:true}).click()]);
  await toggle.waitFor();await toggle.click();await page.getByText('运行中',{exact:true}).waitFor();
  assert.equal(starts,1);checks.push('explicit start recovers page');
  await page.getByRole('button',{name:'重启后端',exact:true}).click();
  await Promise.all([page.waitForNavigation({waitUntil:'load'}),page.getByRole('button',{name:'确认重启',exact:true}).click()]);
  await toggle.waitFor();assert.equal(starts,2);assert.equal(stops,2);checks.push('restart sends confirmed command');
  await toggle.hover();await page.waitForTimeout(250);
  const box=await toggle.boundingBox();
  await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.down();
  await page.mouse.move(8,300,{steps:20});await page.mouse.up();
  await page.mouse.move(500,600);await page.waitForTimeout(250);
  assert.equal(await toggle.getAttribute('data-edge'),'left');
  let rect=await toggle.boundingBox();assert(rect.x<0&&rect.x+rect.width>10,'left half-hidden');
  await page.reload();await toggle.waitFor();await page.waitForTimeout(250);
  assert.equal(await toggle.getAttribute('data-edge'),'left');
  rect=await toggle.boundingBox();assert(rect.x<0,'position restored half-hidden');checks.push('drag, half hide, and position persistence');
  if(evidence)await page.screenshot({path:resolve(evidence,'runtime-edge-left.png'),animations:'disabled'});
  await toggle.focus();await page.waitForTimeout(250);
  rect=await toggle.boundingBox();assert(rect.x>=0,'keyboard focus reveals button');
  await page.keyboard.press('Enter');await page.getByRole('dialog').waitFor();
  await page.keyboard.press('Escape');assert.equal(await toggle.getAttribute('aria-expanded'),'false');checks.push('keyboard reveal, open, and escape');
  await page.setViewportSize({width:320,height:568});await toggle.click();
  const dialog=await page.getByRole('dialog').boundingBox();assert(dialog.x>=0&&dialog.x+dialog.width<=320);checks.push('mobile viewport clamps panel');
  await page.getByRole('button',{name:'关闭控制面板',exact:true}).click();
  await page.setViewportSize({width:1000,height:760});
  for(const [edge,x,y] of [['right',998,300],['top',500,2],['bottom',600,758]]){
    await toggle.hover();await page.waitForTimeout(250);
    const b=await toggle.boundingBox();
    await page.mouse.move(b.x+b.width/2,b.y+b.height/2);await page.mouse.down();
    await page.mouse.move(x,y,{steps:16});await page.mouse.up();
    await toggle.evaluate(node=>node.blur());await page.mouse.move(450,450);await page.waitForTimeout(250);
    assert.equal(await toggle.getAttribute('data-edge'),edge);
    const r=await toggle.boundingBox();
    assert(edge==='right'?r.x+r.width>1000:edge==='top'?r.y<0:r.y+r.height>760,'edge is half hidden');
    checks.push(edge+' edge docking');
  }
  await page.request.post(new URL('/__fixture_idle',url).href);
  await toggle.click();await page.getByText('已停止 · 按需启动',{exact:true}).waitFor();
  const pauseIdle=page.getByRole('button',{name:'停止后端',exact:true});assert.equal(await pauseIdle.isEnabled(),true);
  await pauseIdle.click();
  await Promise.all([page.waitForNavigation({waitUntil:'load'}),page.getByRole('button',{name:'确认停止',exact:true}).click()]);
  await page.getByText('已手动停止',{exact:true}).waitFor();
  checks.push('idle backend can also be manually paused');
  assert.equal(pageErrors.length,0,pageErrors.join('\n'));
  await page.close();
 });
 console.log(JSON.stringify({checks,pageErrors:pageErrors.length,controls},null,2));
} finally {server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
