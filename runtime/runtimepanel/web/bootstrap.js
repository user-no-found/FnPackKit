import { installPanel } from './panel.js';
const base=new URL('./',import.meta.url);
try {
 const response=await fetch(new URL('manifest',base),{credentials:'same-origin',cache:'no-store'});
 if(!response.ok) throw new Error('runtime manifest unavailable');
 const config=await response.json();
 if(typeof config.prefix!=='string' || typeof config.name!=='string') throw new Error('invalid runtime manifest');
 installPanel(config);
} catch(error) {
 console.error('FnPackKit runtime controls unavailable:',error);
}
