import { observeViewport } from './viewport.js';

// Application selectors own behavior; this module only projects their controls.
export function installExtensions(container, actions, close, place) {
  const groups=new Map();
  const entries=[];
  const targetFor=selector=>{
    try {const node=document.querySelector(selector);return typeof node?.click==='function'?node:null;}
    catch {return null;}
  };
  for(const action of Array.isArray(actions)?actions:[]) {
    if(!action || typeof action.id!=='string' || typeof action.label!=='string' || typeof action.selector!=='string')continue;
    if(entries.some(entry=>entry.action.id===action.id))continue;
    const key=action.group || action.id;
    let group=groups.get(key);
    if(!group) {
      group=document.createElement('div');group.className='extension-group';
      group.dataset.group=key;
      group.style.gridTemplateColumns=`repeat(${[2,3].includes(action.columns)?action.columns:1},minmax(0,1fr))`;
      container.append(group);groups.set(key,group);
    }
    const button=document.createElement('button');button.type='button';
    button.textContent=action.label;button.dataset.action=action.id;
    button.addEventListener('click',()=>{
      const target=targetFor(action.selector);
      if(!target || target.disabled){button.disabled=true;return;}
      if(!action.keep_open)close();
      target.click();sync();place();
    });
    group.append(button);entries.push({action,button});
  }
  let touch=false;
  function sync() {
    for(const {action,button} of entries) {
      const target=targetFor(action.selector);
      button.hidden=action.audience==='touch'?!touch:action.audience==='pointer'?touch:false;
      button.disabled=!target || Boolean(target.disabled);
      if(target) {
        if(!target.hidden)target.hidden=true;
        if(target.style.display!=='none')target.style.display='none';
      }
      const pressed=target?.getAttribute('aria-pressed');
      if(pressed==='true'||pressed==='false')button.setAttribute('aria-pressed',pressed);
      else button.removeAttribute('aria-pressed');
    }
    for(const group of groups.values())group.hidden=[...group.children].every(button=>button.hidden);
  }
  const observer=new MutationObserver(sync);
  observer.observe(document.body,{childList:true,subtree:true,attributes:true,attributeFilter:['disabled','aria-pressed']});
  observeViewport(view=>{touch=view.coarse&&view.touch;sync();place();});
  sync();
}
