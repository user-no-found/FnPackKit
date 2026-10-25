import { installPresentation } from './presentation.js';
import { installExtensions } from './extensions.js';

const states = { running:'运行中', stopped:'已停止 · 按需启动', paused:'已手动停止', starting:'正在启动…', stopping:'正在停止…', unavailable:'状态暂不可用' };
function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text) node.textContent = text;
  if (className) node.className = className;
  return node;
}

export function installPanel(config) {
  if (document.querySelector('[data-fnpackkit-runtime]')) return;
  const host = element('div');
  host.dataset.fnpackkitRuntime = '';
  const shadow = host.attachShadow({ mode:'open' });
  const style = element('link');
  style.rel='stylesheet'; style.href=new URL('./panel.css',import.meta.url).href;
  shadow.append(style);
  const toggle = element('button',null,'toggle');
  const icon=document.createElementNS('http://www.w3.org/2000/svg','svg');
  icon.setAttribute('viewBox','0 0 24 24');icon.setAttribute('width','22');icon.setAttribute('height','22');icon.setAttribute('aria-hidden','true');
  const glyph=document.createElementNS('http://www.w3.org/2000/svg','path');
  glyph.setAttribute('d','M12 3v9M6.4 5.6a8 8 0 1 0 11.2 0');glyph.setAttribute('fill','none');glyph.setAttribute('stroke','currentColor');glyph.setAttribute('stroke-width','2');glyph.setAttribute('stroke-linecap','round');
  icon.append(glyph);toggle.append(icon);
  toggle.type='button'; toggle.setAttribute('aria-label','后端控制'); toggle.setAttribute('aria-expanded','false');
  const panel = element('section',null,'panel');
  panel.hidden=true; panel.setAttribute('aria-label',config.name+' 后端控制'); panel.setAttribute('role','dialog');
  const title = element('strong',config.name);
  const close = element('button','×','close'); close.type='button'; close.setAttribute('aria-label','关闭控制面板');
  const head = element('header'); head.append(title,close);
  const status = element('p','正在读取状态…','status'); status.setAttribute('role','status'); status.setAttribute('aria-live','polite');
  const actions = element('div',null,'actions');
  const commandBar = element('div',null,'commands');
  const buttons = {};
  const commands = new Map();
  let busy=false;
  let current=null;
  let pending=null;
  const notices=element('p','','notice'); notices.setAttribute('role','alert');
  const confirm = element('div',null,'confirm'); confirm.hidden=true;
  const warning=element('p');
  const proceed=element('button','确认停止','danger'); proceed.type='button';
  const cancel=element('button','取消'); cancel.type='button';
  confirm.append(warning,proceed,cancel);
  for (const [id,label] of [['start','启动后端'],['stop','停止后端'],['restart','重启后端']]) {
    const button=element('button',label); button.type='button'; buttons[id]=button;
    button.addEventListener('click',()=>requestAction(id)); actions.append(button);
  }
  for (const item of Array.isArray(config.commands) ? config.commands : []) {
    if (!item || typeof item.id!=='string' || typeof item.label!=='string' || commands.has(item.id)) continue;
    if (['start','stop','restart'].includes(item.id)) continue;
    const command={id:item.id,label:item.label,confirm:Boolean(item.confirm),
      warning:typeof item.warning==='string'?item.warning:''};
    const button=element('button',command.label); button.type='button'; buttons[command.id]=button;
    button.dataset.command=command.id;
    button.addEventListener('click',()=>requestAction(command.id)); commandBar.append(button);
    commands.set(command.id,command);
  }
  const refresh=element('button','刷新状态','text'); refresh.type='button'; refresh.addEventListener('click',()=>refreshStatus());
  const extensions=element('div',null,'extensions');
  const note=element('p','停止仅影响当前账号的后端，保留应用数据。完全退出请使用应用中心。','footnote');
  panel.append(head,status,actions,commandBar,confirm,extensions,notices,refresh,note);
  shadow.append(toggle,panel); document.body.append(host);
  const drag=installPresentation(host,toggle,panel,config,()=>{open(false);drag.focusLauncher();});
  function open(value) {
    panel.hidden=!value; toggle.dataset.open=String(value); toggle.setAttribute('aria-expanded',String(value));
    if (value) { drag.place(); refreshStatus(); }
    else { confirm.hidden=true; pending=null; drag.place(); }
  }
  addEventListener('fnpackkit:open-controls',()=>{
    open(true);
    requestAnimationFrame(()=>{if(!panel.hidden)close.focus({preventScroll:true});});
  });
  addEventListener('fnpackkit:notice',event=>{
    const message=event.detail?.message;
    if(typeof message==='string' && message.length<=512){notices.textContent=message;drag.place();}
  });
  toggle.addEventListener('click',()=>open(panel.hidden));
  close.addEventListener('click',()=>{open(false);drag.focusLauncher();});
  shadow.addEventListener('keydown',e=>{
    if(e.key==='Escape'){e.preventDefault();e.stopPropagation();open(false);drag.focusLauncher();}
    if(e.key==='Tab' && !panel.hidden && host.dataset.compact==='true'){
      const focusable=[...panel.querySelectorAll('button')].filter(node=>!node.disabled&&node.getClientRects().length);
      const index=focusable.indexOf(shadow.activeElement);
      e.preventDefault();e.stopPropagation();
      focusable[(index+(e.shiftKey?-1:1)+focusable.length)%focusable.length]?.focus();
    }
  });
  function render() {
    status.textContent=states[current?.state] || '状态暂不可用';
    toggle.dataset.state=current?.state || 'unavailable';
    toggle.title=config.name+'：'+status.textContent;
    buttons.start.disabled=busy || current?.state==='running' && !current?.paused;
    buttons.stop.disabled=busy || !current || current.state==='paused';
    buttons.restart.disabled=busy || !current;
    for(const id of commands.keys()) buttons[id].disabled=busy;
    refresh.disabled=busy; proceed.disabled=busy; cancel.disabled=busy;
  }
  async function query(command) {
    const options={credentials:'same-origin',cache:'no-store'};
    if(command) Object.assign(options,{method:'POST',headers:{'Content-Type':'application/json','X-FnPackKit-Control':'1'},body:JSON.stringify(command)});
    const response=await fetch(config.prefix+'/_runtime/control',options);
    const result=await response.json();
    if(result.schema===1 && states[result.state]) {current=result;render();}
    if(!response.ok || result.failed || result.schema!==1 || !states[result.state]) throw new Error('操作未完成，请刷新状态后重试。');
    return result;
  }
  async function refreshStatus() {
    if(busy) return;
    try {await query();notices.textContent='';} catch {notices.textContent='无法读取后端状态，请重新打开应用或稍后重试。';}
  }
  function requestAction(action) {
    if(busy) return;
    const command=commands.get(action);
    if(action==='start' || command && !command.confirm) {execute(action);return;}
    pending=action;confirm.hidden=false;
    if(command) {
      warning.textContent=command.warning ||
        command.label+'会停止当前后端正在运行的任务，同步最新代码并按需重启。应用数据会保留。是否继续？';
      proceed.textContent='确认'+command.label;
    } else {
      warning.textContent=(action==='restart'?'重启':'停止')+'后端会结束当前账号正在运行的终端、AI 或其他后台任务。应用数据会保留。是否继续？';
      proceed.textContent=action==='restart'?'确认重启':'确认停止';
    }
    proceed.focus();drag.place();
  }
  cancel.addEventListener('click',()=>{confirm.hidden=true;pending=null;drag.place();});
  proceed.addEventListener('click',()=>{if(pending)execute(pending);});
  async function execute(action) {
    const command=commands.get(action);
    busy=true;notices.textContent='';render();
    status.textContent=command?command.label+'…':action==='start'?'正在启动…':action==='restart'?'正在重启…':'正在停止…';
    try {
      await query({action,confirm:action!=='start'});
      confirm.hidden=true;pending=null;
      location.replace(config.prefix+'/');
    } catch {
      notices.textContent=command
        ? command.label+'未完成，请刷新状态或查看应用数据目录下的更新日志。'
        : '操作未完成，后端可能仍在处理请求或保存数据。请刷新状态后重试。';
    }
    finally {busy=false;render();drag.place();}
  }
  installExtensions(extensions,config.actions,()=>open(false),()=>drag.place());
  document.addEventListener('visibilitychange',()=>{if(!document.hidden && !panel.hidden)refreshStatus();});
  if(document.documentElement.hasAttribute('data-runtime-page')) open(true);
  else refreshStatus();
}
