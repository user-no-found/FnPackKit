import { readFile } from 'node:fs/promises';

async function moduleURL(name) {
  let source = await readFile(new URL(`../runtime/runtimepanel/web/${name}`, import.meta.url), 'utf8');
  for (const match of [...source.matchAll(/from '(\.\/[a-z-]+\.js)'/g)]) {
    source = source.replace("'"+match[1]+"'", JSON.stringify(await moduleURL(match[1].slice(2))));
  }
  return 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
}
export async function panelModule(name) { return import(await moduleURL(name)); }

export class Clock {
  time = 1000;
  next = 1;
  jobs = new Map();
  history = new Map();
  now = () => this.time;
  schedule = (run, delay) => {
    const id = this.next++;
    this.jobs.set(id, { run, at:this.time + delay });
    this.history.set(id, run);
    return id;
  };
  cancel = id => this.jobs.delete(id);
  tick(delay) {
    const until = this.time + delay;
    for (;;) {
      const next = [...this.jobs].filter(([, job]) => job.at <= until).sort((a,b) => a[1].at-b[1].at)[0];
      if (!next) break;
      this.time = next[1].at;
      this.jobs.delete(next[0]);
      next[1].run();
    }
    this.time = until;
  }
  jump(delay) { this.time += delay; }
  stale(id) { this.history.get(id)(); }
}

export class PointerButton {
  dataset = {};
  style = {};
  listeners = new Map();
  captured = new Set();
  addEventListener(name, run, capture) {
    const list = this.listeners.get(name) || [];
    list.push({ run, capture:capture === true });
    this.listeners.set(name, list);
  }
  emit(name, fields = {}) {
    const event = { button:0, pointerId:1, isPrimary:true, pointerType:'mouse', isTrusted:true,
      clientX:390, clientY:200, detail:1, stopped:false, defaultPrevented:false,
      preventDefault() { this.defaultPrevented = true; },
      stopImmediatePropagation() { this.stopped = true; }, ...fields };
    for (const item of [...(this.listeners.get(name) || [])].sort((a,b) => Number(b.capture)-Number(a.capture))) {
      item.run(event);
      if (event.stopped) break;
    }
    return event;
  }
  setPointerCapture(id) { this.captured.add(id); }
  hasPointerCapture(id) { return this.captured.has(id); }
  releasePointerCapture(id) {
    this.captured.delete(id);
    this.emit('lostpointercapture', { pointerId:id });
  }
  getBoundingClientRect() { throw new Error('drag must not use the transformed rectangle'); }
}
