import test from 'node:test';
import assert from 'node:assert/strict';
import { Clock, panelModule } from './runtime_panel_test_support.mjs';

const { installPresentation } = await panelModule('presentation.js');
const { createInteraction } = await panelModule('state.js');
const { installExtensions } = await panelModule('extensions.js');
class Node {
  listeners = new Map();
  style = {};
  dataset = {};
  hidden = false;
  isConnected = true;
  tagName = 'DIV';
  offsetWidth = 44;
  offsetHeight = 44;
  focused = 0;
  children = [];
  addEventListener(name, run) {
    this.listeners.set(name, [...(this.listeners.get(name) || []), run]);
  }
  removeEventListener(name, run) {
    this.listeners.set(name, (this.listeners.get(name) || []).filter(item => item !== run));
  }
  emit(name, fields = {}) {
    const event = { isTrusted:true, defaultPrevented:false, propagationStopped:false, movementX:0, movementY:0,
      composedPath:() => [this], preventDefault() { this.defaultPrevented=true; },
      stopPropagation() { this.propagationStopped=true; }, ...fields };
    for (const run of this.listeners.get(name) || []) run(event);
    return event;
  }
  setAttribute(name, value) { this[name]=value; }
  getAttribute(name) { return this[name] ?? null; }
  removeAttribute(name) { delete this[name]; }
  append(...nodes) { this.children.push(...nodes); }
  getClientRects() { return this.hidden ? [] : [this.getBoundingClientRect()]; }
  getBoundingClientRect() {
    const left = parseFloat(this.style.left) || 0, top = parseFloat(this.style.top) || 0;
    return {left,top,right:left+this.offsetWidth,bottom:top+this.offsetHeight};
  }
  focus() { this.focused++; }
}
function fixture({ toolbar=false } = {}) {
  const clock = new Clock(), frames = [], observers = [], selectors = new Map();
  const doc = new Node(), win = new Node(), host = new Node(), button = new Node(), panel = new Node();
  host.shadowRoot = new Node();
  panel.offsetWidth=320; panel.offsetHeight=250; panel.hidden=true;
  button.tagName='BUTTON';
  const launcher = new Node(); launcher.tagName='BUTTON'; launcher.disabled=true;
  if (toolbar) selectors.set('#launcher', launcher);
  doc.activeElement = new Node(); doc.body = new Node();
  doc.createElement = () => new Node();
  doc.querySelector = selector => selectors.get(selector) || null;
  globalThis.document = doc;
  globalThis.window = win;
  globalThis.innerWidth = 400; globalThis.innerHeight = 600;
  Object.defineProperty(globalThis, 'navigator', { value:{maxTouchPoints:0}, configurable:true });
  globalThis.matchMedia = () => ({ matches:false, addEventListener() {} });
  globalThis.requestAnimationFrame = run => frames.push(run);
  globalThis.addEventListener = (...args) => win.addEventListener(...args);
  globalThis.getComputedStyle = () => ({visibility:'visible',display:'block',overflowX:'visible',overflowY:'visible'});
  globalThis.localStorage = { getItem() { return null; }, setItem() {} };
  globalThis.ResizeObserver = class {
    constructor(run) { observers.push(run); }
    observe() {} unobserve() {}
  };
  globalThis.MutationObserver = class {
    constructor(run) { observers.push(run); }
    observe() {} disconnect() {}
  };
  let ui, closed = 0;
  const state = createInteraction({ now:clock.now, schedule:clock.schedule, cancel:clock.cancel,
    onClose() { panel.hidden=true; closed++; },
    onChange(value) { panel.hidden=value!=='open'; if (value==='half') ui?.settle(); ui?.place(); } });
  ui = installPresentation(host,button,panel,{prefix:'/app/test',layout:{launcher:'#launcher'}},state);
  function flush() { for (let i=0; frames.length && i<10; i++) frames.splice(0).forEach(run => run()); }
  flush();
  return { clock, state, ui, doc, win, host, button, panel, launcher, frames, observers, selectors, flush,
    get closed() { return closed; } };
}

test('layout/render observers, viewport updates, stationary focus/hover never extend deadlines', () => {
  const f = fixture();
  f.state.open();
  const deadline = f.state.deadline;
  for (let i=0; i<4; i++) {
    f.clock.tick(1000);
    f.observers.forEach(run => run()); f.ui.place(); f.flush();
    f.panel.emit('focus'); f.panel.emit('pointermove');
    f.button.emit('focus'); f.button.emit('pointerenter');
    assert.equal(f.state.deadline, deadline);
  }
  f.clock.tick(1000);
  assert.equal(f.state.value, 'half');
  assert.equal(f.closed, 1);
});

test('only trusted panel input activity extends the five-second window', () => {
  const f = fixture();
  f.state.open();
  f.clock.tick(4500);
  f.panel.emit('input', {isTrusted:false});
  assert.equal(f.state.deadline, 6000);
  f.panel.emit('input');
  assert.equal(f.state.deadline, 10500);
  f.clock.tick(4500);
  f.panel.emit('touchmove', {isTrusted:false});
  assert.equal(f.state.deadline, 10500);
  f.panel.emit('touchmove');
  assert.equal(f.state.deadline, 15000);
  f.panel.emit('scroll');
  assert.equal(f.state.deadline, 15000);
  f.clock.tick(4999);
  assert.equal(f.state.value, 'open');
  f.clock.tick(1);
  assert.equal(f.state.value, 'half');
});

for (const tag of ['INPUT','TEXTAREA','SELECT','contenteditable','shadow-textarea']) {
  test(`pointer control and toolbar preserve actual ${tag} focus while keyboard may navigate`, () => {
    const f = fixture({toolbar:true});
    const editor = new Node();
    editor.tagName=tag.toUpperCase(); editor.isContentEditable=tag==='contenteditable';
    f.doc.activeElement=editor;
    if (tag==='shadow-textarea') {
      editor.shadowRoot={activeElement:new Node()}; editor.shadowRoot.activeElement.tagName='TEXTAREA';
    }
    f.doc.emit('pointerdown');
    const target={closest:() => f.button};
    assert.equal(f.host.shadowRoot.emit('pointerdown', {target}).defaultPrevented, true);
    assert.equal(f.launcher.emit('pointerdown').defaultPrevented, true);
    f.ui.focusControl(f.button); f.ui.focusLauncher();
    assert.equal(f.button.focused + f.launcher.focused, 0);
    f.doc.emit('keydown', {key:'Tab'});
    f.ui.focusLauncher();
    assert.equal(f.launcher.focused, 1);
  });
}

test('Escape closes from a business editor without blurring or leaking the key to business', () => {
  const f = fixture();
  const editor = new Node(); editor.tagName='TEXTAREA'; f.doc.activeElement=editor;
  f.state.open();
  const event=f.doc.emit('keydown', {key:'Escape',composedPath:() => [editor]});
  assert.equal(event.defaultPrevented, true);
  assert.equal(event.propagationStopped, true);
  assert.equal(f.state.value, 'half');
  assert.equal(f.doc.activeElement, editor);
  assert.equal(f.button.focused, 0);
  const repeat=f.doc.emit('keydown', {key:'Escape',repeat:true,composedPath:() => [editor]});
  assert.equal(repeat.propagationStopped, true);
  const release=f.doc.emit('keyup', {key:'Escape',composedPath:() => [editor]});
  assert.equal(release.defaultPrevented, true);
  assert.equal(release.propagationStopped, true);
  const closed=f.doc.emit('keydown', {key:'Escape',composedPath:() => [editor]});
  assert.equal(closed.defaultPrevented, false);
  assert.equal(f.doc.emit('keyup', {key:'Escape'}).defaultPrevented, false);
});

test('lost Escape release ownership never consumes a later business key after blur or a fresh press', () => {
  for (const loseRelease of [f => f.win.emit('blur'), f => f.doc.emit('keydown', {key:'Escape'})]) {
    const f = fixture();
    f.state.open();
    f.doc.emit('keydown', {key:'Escape'});
    loseRelease(f);
    assert.equal(f.doc.emit('keyup', {key:'Escape'}).propagationStopped, false);
  }
});

test('Escape from shared keyboard navigation restores launcher focus but never reveals half', () => {
  const f = fixture({toolbar:true});
  f.state.open();
  f.doc.emit('keydown', {key:'Escape',composedPath:() => [f.panel,f.host]});
  assert.equal(f.state.value, 'half');
  assert.equal(f.launcher.focused, 1);
  assert.equal(f.panel['aria-modal'], 'false');
});

test('KeepOpen actual extension target.click is not an outside user action; normal extensions still close', () => {
  for (const keepOpen of [true, false]) {
    const f = fixture(), container = new Node(), target = new Node();
    let businessClicks = 0;
    target.click = () => {
      businessClicks++;
      f.doc.emit('click', {isTrusted:false,composedPath:() => [target,f.doc.body]});
    };
    f.selectors.set('#business-target', target);
    installExtensions(container, [{id:'setting',label:'Setting',selector:'#business-target',keep_open:keepOpen}],
      () => f.state.close(), f.ui.place);
    f.state.open();
    container.children[0].children[0].emit('click');
    assert.equal(businessClicks, 1);
    assert.equal(f.state.value, keepOpen ? 'open' : 'half');
    assert.equal(f.closed, keepOpen ? 0 : 1);
  }
});

test('synthetic outside events never close; genuine outside pointerdown and click close immediately', () => {
  for (const name of ['pointerdown','click']) {
    const f = fixture();
    f.state.open();
    f.doc.emit(name, {isTrusted:false,composedPath:() => [new Node()]});
    assert.equal(f.state.value, 'open');
    f.doc.emit(name, {isTrusted:true,composedPath:() => [new Node()]});
    assert.equal(f.state.value, 'half');
    assert.equal(f.closed, 1);
  }
});

test('outside closes immediately while a declared launcher remains the sole visible entry', () => {
  const f = fixture({toolbar:true});
  assert.equal(f.launcher.disabled, false);
  assert.equal(f.button.hidden, true);
  f.state.open();
  f.doc.emit('pointerdown', {composedPath:() => [f.launcher]});
  assert.equal(f.state.value, 'open');
  f.doc.emit('pointerdown', {composedPath:() => [new Node()]});
  assert.equal(f.state.value, 'half');
  f.selectors.delete('#launcher'); f.ui.place(); f.flush();
  assert.equal(f.button.hidden, false);
  assert.equal((f.launcher.listeners.get('pointerdown') || []).length, 0);
});
