import test from 'node:test';
import assert from 'node:assert/strict';
import { Clock, PointerButton, panelModule } from './runtime_panel_test_support.mjs';

const { draggable } = await panelModule('drag.js');
const { createInteraction } = await panelModule('state.js');
const geometry = await panelModule('geometry.js');
function fixture(saved = null, pointerType = 'mouse') {
  const store = new Map(saved ? [['position',JSON.stringify(saved)]] : []);
  globalThis.localStorage = { getItem:key => store.get(key), setItem:(key,value) => store.set(key,value) };
  const button = new PointerButton(), clock = new Clock();
  let view = { left:0, top:0, width:400, height:600 }, drag;
  const state = createInteraction({ now:clock.now, schedule:clock.schedule, cancel:clock.cancel,
    onChange:value => { button.dataset.uiState=value; if (value === 'half') drag?.settle(); }, onClose() {} });
  drag = draggable(button, 'position', () => view, { now:clock.now, activity:state.activity, dragging:state.drag, placePanel() {} });
  let clicks = 0;
  button.addEventListener('click', () => { clicks++; state.activate(); });
  drag.place();
  return { button, clock, state, drag, store, get clicks() { return clicks; },
    emit:(name,values) => button.emit(name, { pointerType, ...values }), resize:value => { view=value; drag.place(); } };
}

for (const pointerType of ['mouse','touch','pen']) {
  test(`${pointerType} drags from the logical half origin, snaps nearest edge, never clicks open`, () => {
    const f = fixture(null, pointerType);
    f.state.close();
    f.emit('pointerdown', { clientX:390, clientY:200 });
    f.emit('pointermove', { clientX:380, clientY:210 });
    assert.equal(f.button.style.left, '346px');
    assert.equal(f.button.dataset.dragging, 'true');
    assert.equal(f.state.value, 'revealed');
    f.emit('pointerup');
    assert.equal(f.button.dataset.edge, 'right');
    assert.equal(f.button.dataset.dragging, undefined);
    assert.equal(f.emit('click').defaultPrevented, true);
    assert.equal(f.clicks, 0);
    f.clock.tick(4999);
    assert.equal(f.state.value, 'revealed');
    f.clock.tick(1);
    assert.equal(f.state.value, 'half');
    f.emit('pointerdown'); f.emit('pointerup'); f.emit('click');
    assert.equal(f.state.value, 'revealed');
    assert.equal(f.clicks, 1);
  });
}

test('all four nearest edges snap without requiring a near-edge threshold', () => {
  const view = { left:10, top:20, width:500, height:500 };
  for (const [position, edge] of [
    [{x:.2,y:.5,edge:''},'left'], [{x:.8,y:.5,edge:''},'right'],
    [{x:.5,y:.2,edge:''},'top'], [{x:.5,y:.8,edge:''},'bottom'],
  ]) assert.equal(geometry.nearestEdge(position, view).edge, edge);
});

test('a secondary finger cannot replace or finish the primary gesture', () => {
  const f = fixture(null, 'touch');
  f.emit('pointerdown', { pointerId:7 });
  f.emit('pointerdown', { pointerId:8, isPrimary:false, clientX:10 });
  f.emit('pointermove', { pointerId:8, isPrimary:false, clientX:20 });
  f.emit('pointerup', { pointerId:8, isPrimary:false });
  assert.equal(f.button.dataset.dragging, undefined);
  f.emit('pointermove', { pointerId:7, clientX:120 });
  assert.equal(f.button.dataset.dragging, 'true');
  f.emit('pointerup', { pointerId:7 });
  assert.equal(f.button.dataset.edge, 'left');
  assert.equal(f.button.captured.size, 0);
});

for (const ending of ['pointercancel','lostpointercapture']) {
  test(`${ending} clears capture/drag and does not permanently suppress the next click`, () => {
    const f = fixture();
    f.emit('pointerdown'); f.emit('pointermove', { clientX:150 });
    f.emit(ending);
    assert.equal(f.button.dataset.dragging, undefined);
    assert.equal(f.button.captured.size, 0);
    assert.equal(f.emit('click').defaultPrevented, true);
    f.emit('pointerdown'); f.emit('pointerup'); f.emit('click');
    assert.equal(f.clicks, 1);
    assert.equal(f.state.value, 'open');
  });
}

test('suppression is pointer-bounded, time-bounded, and never swallows a keyboard click', () => {
  for (const click of [{detail:0}, {pointerId:99}, {delay:601}]) {
    const f = fixture();
    f.emit('pointerdown'); f.emit('pointermove', { clientX:150 }); f.emit('pointerup');
    if (click.delay) f.clock.tick(click.delay);
    assert.equal(f.emit('click', click).defaultPrevented, false);
    assert.equal(f.clicks, 1);
  }
});

test('stationary held drag still idles and clears ownership; late pointermove cannot revive it', () => {
  const f = fixture();
  f.emit('pointerdown'); f.emit('pointermove', { clientX:150 });
  f.clock.tick(5000);
  assert.equal(f.state.value, 'half');
  assert.equal(f.button.dataset.dragging, undefined);
  f.emit('pointermove', { clientX:100 }); f.emit('pointerup');
  assert.equal(f.state.value, 'half');
  assert.equal(f.emit('click').defaultPrevented, true);
});

test('expired frozen drag checks before movement and release without a stale-owner race', () => {
  for (const ending of ['pointermove','pointerup']) {
    const f = fixture();
    f.emit('pointerdown'); f.emit('pointermove', { clientX:150 });
    f.clock.jump(6000);
    f.emit(ending, { clientX:100 });
    assert.equal(f.state.value, 'half');
    assert.equal(f.button.dataset.dragging, undefined);
  }
});

test('local position remains normalized and viewport/presentation changes never renew deadlines', () => {
  const f = fixture({x:1.5,y:-.5,edge:'bottom'});
  assert.equal(f.button.style.left, '356px');
  assert.equal(f.button.style.top, '556px');
  const deadline = f.state.deadline;
  f.resize({left:20,top:30,width:320,height:250});
  assert.equal(f.button.style.left, '296px');
  assert.equal(f.button.style.top, '236px');
  assert.equal(f.state.deadline, deadline);
  f.clock.tick(5000);
  assert.equal(JSON.parse(f.store.get('position')).edge, 'right');
});

test('nonmodal float clamps inside viewport/keyboard/toolbar bounds beside its anchor', () => {
  const view = {left:20,top:30,width:390,height:360};
  const point = geometry.floatingPosition({left:366,right:410,top:180,bottom:224},320,280,view);
  assert.ok(point.x >= 28 && point.x + 320 <= 402);
  assert.ok(point.y >= 38 && point.y + 280 <= 382);
  assert.notEqual(point.y, view.top + view.height);
  assert.equal(geometry.storedPosition({x:0,y:0,edge:'private'}), null);
});
