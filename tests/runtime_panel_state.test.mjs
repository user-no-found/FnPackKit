import test from 'node:test';
import assert from 'node:assert/strict';
import { Clock, panelModule } from './runtime_panel_test_support.mjs';

const { createInteraction } = await panelModule('state.js');
function fixture() {
  const clock = new Clock(), changes = [], closed = [];
  const state = createInteraction({ now:clock.now, schedule:clock.schedule, cancel:clock.cancel,
    onChange:value => changes.push(value), onClose:reason => closed.push(reason) });
  return { clock, state, changes, closed };
}

test('initial ordinary full launcher expires at exactly five seconds without presentation renewal', () => {
  const { clock, state, closed } = fixture();
  assert.equal(state.value, 'revealed');
  const deadline = state.deadline;
  for (let i=0; i<49; i++) { clock.tick(100); state.check(); }
  assert.equal(state.deadline, deadline);
  assert.equal(state.value, 'revealed');
  clock.tick(99);
  assert.equal(state.value, 'revealed');
  clock.tick(1);
  assert.equal(state.value, 'half');
  assert.deepEqual(closed, ['idle']);
});

test('half first activation reveals for three seconds and never opens in that activation', () => {
  const { clock, state, changes } = fixture();
  state.close();
  state.activate();
  assert.equal(state.value, 'revealed');
  assert.ok(!changes.includes('open'));
  clock.tick(2999);
  assert.equal(state.value, 'revealed');
  clock.tick(1);
  assert.equal(state.value, 'half');
  state.activate();
  state.activate();
  assert.equal(state.value, 'open');
  clock.tick(4999);
  assert.equal(state.value, 'open');
  clock.tick(1);
  assert.equal(state.value, 'half');
});

test('genuine revealed activity keeps the three-second window, open input keeps five', () => {
  const { clock, state } = fixture();
  state.close(); state.activate();
  clock.tick(2500); state.activity();
  clock.tick(2999);
  assert.equal(state.value, 'revealed');
  clock.tick(1);
  assert.equal(state.value, 'half');
  state.open();
  for (let i=0; i<4; i++) { clock.tick(4500); state.activity(); }
  clock.tick(4999);
  assert.equal(state.value, 'open');
  clock.tick(1);
  assert.equal(state.value, 'half');
});

test('close calls the UI owner before half and does not let retained focus revive it', () => {
  const clock = new Clock(), effects = [];
  const state = createInteraction({ now:clock.now, schedule:clock.schedule, cancel:clock.cancel,
    onChange:value => effects.push(value), onClose:() => effects.push('resetPending') });
  for (const reason of ['close','escape','outside','idle']) {
    state.open(); state.close(reason);
    assert.deepEqual(effects.slice(-2), ['resetPending','half']);
    state.activity(); state.check();
    assert.equal(state.value, 'half');
    assert.equal(state.deadline, null);
  }
});

test('superseded timers cannot close a newly opened or refreshed interaction', () => {
  const { clock, state, closed } = fixture();
  const initial = clock.next-1;
  clock.tick(1000); state.open();
  const opened = clock.next-1;
  clock.tick(1000); state.activity();
  clock.stale(initial); clock.stale(opened);
  assert.equal(state.value, 'open');
  assert.deepEqual(closed, []);
  state.close(); state.activate();
  clock.stale(opened);
  assert.equal(state.value, 'revealed');
});

test('frozen lifecycle checks absolute elapsed time without treating resume as input', () => {
  const { clock, state, closed } = fixture();
  state.open();
  clock.jump(5000);
  state.check();
  assert.equal(state.value, 'half');
  state.check();
  assert.deepEqual(closed, ['idle']);
  state.activate();
  clock.jump(3000);
  // A click after a frozen expired reveal starts a new reveal, not an accidental open.
  state.activate();
  assert.equal(state.value, 'revealed');
  assert.equal(state.deadline, clock.now()+3000);
});

test('late input after elapsed deadline closes rather than resurrecting the old UI owner', () => {
  const { clock, state, closed } = fixture();
  state.open();
  clock.jump(6000); state.activity();
  assert.equal(state.value, 'half');
  assert.deepEqual(closed, ['idle']);
  assert.equal(clock.jobs.size, 0);
});

test('drag uses the general five-second deadline even when it began half-hidden', () => {
  const { clock, state } = fixture();
  state.close(); state.drag();
  assert.equal(state.value, 'revealed');
  clock.tick(3000);
  assert.equal(state.value, 'revealed');
  state.drag();
  clock.tick(4999);
  assert.equal(state.value, 'revealed');
  clock.tick(1);
  assert.equal(state.value, 'half');
  state.open(); state.drag();
  assert.equal(state.value, 'open');
});

test('an early timer callback rearms against the original absolute deadline', () => {
  const { clock, state } = fixture();
  const original = state.deadline;
  clock.stale(clock.next-1);
  assert.equal(state.deadline, original);
  clock.tick(5000);
  assert.equal(state.value, 'half');
});
