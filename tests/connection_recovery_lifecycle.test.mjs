import test from 'node:test';
import assert from 'node:assert/strict';
import { browser, timing, settle } from './connection_recovery_harness.mjs';

function attach(t, environment, callbacks = {}) {
  const calls = [];
  const coordinator = environment.create({
    reconnect(context) { calls.push(['reconnect', context]); },
    resync(context) { calls.push(['resync', context]); },
    timing, ...callbacks,
  });
  t.after(() => coordinator.close());
  return { coordinator, calls };
}

test('creation is idle, has a detached read-only snapshot, and never starts business work', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  assert.equal(coordinator.state.state, 'idle');
  assert.deepEqual(Object.keys(coordinator.state).sort(), ['attempt', 'code', 'reason', 'state']);
  const snapshot = coordinator.state;
  try { snapshot.state = 'ready'; } catch (error) { assert.equal(error.name, 'TypeError'); }
  try { coordinator.state = { state: 'ready' }; } catch (error) { assert.equal(error.name, 'TypeError'); }
  await settle(environment, 40);
  assert.equal(coordinator.state.state, 'idle');
  assert.equal(calls.length, 0);
  assert.equal(environment.logs.length, 0);
});

test('callback and timing inputs reject invalid values without a new business authority', async () => {
  const environment = await browser();
  const base = { reconnect() {}, resync() {}, timing };
  for (const options of [undefined, null, {}, { reconnect() {} }, { resync() {} },
    { ...base, reconnect: true }, { ...base, resync: 1 }, { ...base, check: 'check' },
    { ...base, onState: {} }]) {
    assert.throws(() => environment.create(options));
  }
  const invalid = [
    { debounceMs: -1 }, { debounceMs: 0.5 }, { attemptTimeoutMs: 0 },
    { retryBaseMs: 0 }, { retryMaxMs: 0 }, { maxAttempts: 0 }, { maxAttempts: 101 },
    { watchIntervalMs: 0 }, { suspendGapMs: timing.watchIntervalMs },
    { retryBaseMs: 30, retryMaxMs: 20 },
  ];
  for (const name of Object.keys(timing)) {
    for (const value of [NaN, Infinity, '1', true]) invalid.push({ [name]: value });
  }
  for (const override of invalid) {
    assert.throws(() => environment.create({ ...base, timing: { ...timing, ...override } }));
  }
  assert.equal(environment.logs.length, 0);
});

test('manual recover returns void and performs reconnect then resync with one context', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  assert.equal(coordinator.recover(), undefined);
  await settle(environment);
  assert.deepEqual(calls.map(([stage]) => stage), ['reconnect', 'resync']);
  for (const [, context] of calls) {
    assert.equal(context.reason, 'manual');
    assert.equal(context.attempt, 1);
    assert.equal(context.signal.aborted, false);
  }
  assert.equal(coordinator.state.state, 'ready');
});

test('plain visible notifications and ordinary focus never reconnect a healthy foreground page', async t => {
  const environment = await browser();
  const { calls } = attach(t, environment);
  environment.visibility(false);
  environment.emit('pageshow', { persisted: false });
  for (let count = 0; count < 4; count += 1) environment.emit('focus');
  await settle(environment, 60);
  assert.equal(calls.length, 0);
});

test('a hidden-to-visible switch without check conservatively rebuilds the channel', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  environment.visibility(true);
  await settle(environment, 20);
  assert.equal(calls.length, 0);
  environment.visibility(false);
  environment.visibility(false);
  environment.emit('focus');
  await settle(environment);
  assert.deepEqual(calls.map(([stage]) => stage), ['reconnect', 'resync'],
    'without an application check, a foreground return cannot assume a healthy channel');
  assert.equal(coordinator.state.state, 'ready');
});

test('a visibility return after a scheduling suspension recovers', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  environment.visibility(true);
  await settle(environment, 20);
  environment.clock.jump(timing.suspendGapMs + 1);
  environment.visibility(false);
  await settle(environment);
  assert.deepEqual(calls.map(([stage]) => stage), ['reconnect', 'resync']);
  assert.ok(['visible', 'timer-gap'].includes(calls[0][1].reason));
  assert.equal(coordinator.state.state, 'ready');
});

test('pagehide/pageshow and persisted pageshow recover without a document visibility transition', async t => {
  const environment = await browser();
  const { calls } = attach(t, environment);
  environment.emit('pagehide');
  environment.emit('pageshow', { persisted: false });
  await settle(environment);
  assert.equal(calls.filter(([stage]) => stage === 'reconnect').length, 1);
  assert.equal(calls[0][1].reason, 'pageshow');
  environment.emit('pageshow', { persisted: true });
  await settle(environment);
  assert.equal(calls.filter(([stage]) => stage === 'reconnect').length, 2);
});

test('focus recovers a pagehide interruption when the host omits pageshow', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  environment.emit('pagehide');
  await settle(environment);
  assert.equal(calls.length, 0);
  environment.emit('focus');
  await settle(environment);
  assert.equal(calls.filter(([stage]) => stage === 'reconnect').length, 1);
  assert.equal(calls[0][1].reason, 'focus');
  assert.equal(coordinator.state.state, 'ready');
});

test('hidden and offline requests wait, then online recovery runs once in the foreground', async t => {
  const environment = await browser({ hidden: true, online: false });
  const { coordinator, calls } = attach(t, environment);
  coordinator.recover();
  await settle(environment, 20);
  assert.equal(coordinator.state.state, 'waiting');
  assert.equal(calls.length, 0);
  environment.visibility(false);
  await settle(environment, 20);
  assert.equal(coordinator.state.state, 'waiting');
  assert.equal(calls.length, 0);
  environment.network(true);
  await settle(environment);
  assert.deepEqual(calls.map(([stage]) => stage), ['reconnect', 'resync']);
  assert.equal(coordinator.state.state, 'ready');
});

test('offline-to-online recovery does not require a visibility event', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  environment.network(false);
  environment.network(true);
  environment.emit('online');
  await settle(environment);
  assert.equal(calls.filter(([stage]) => stage === 'reconnect').length, 1);
  assert.equal(coordinator.state.state, 'ready');
});

test('a suspended scheduling clock triggers timer-gap recovery, not business silence', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  await settle(environment, 200);
  assert.equal(calls.length, 0, 'regular timer delivery is not a business inactivity timeout');
  environment.clock.jump(timing.suspendGapMs + 1);
  await settle(environment);
  assert.equal(calls[0][1].reason, 'timer-gap');
  assert.equal(coordinator.state.state, 'ready');
});

test('focus after a scheduling suspension is a recovery fallback', async t => {
  const environment = await browser();
  const { coordinator, calls } = attach(t, environment);
  environment.clock.jump(timing.suspendGapMs + 1);
  environment.emit('focus');
  await settle(environment);
  assert.ok(['focus', 'timer-gap'].includes(calls[0][1].reason));
  assert.equal(coordinator.state.state, 'ready');
});

test('close is idempotent and removes timers and browser listeners', async () => {
  const environment = await browser();
  const calls = [];
  const coordinator = environment.create({ reconnect() { calls.push('reconnect'); }, resync() {}, timing });
  assert.ok(environment.listenerCount() > 0);
  coordinator.recover();
  coordinator.close();
  coordinator.close();
  coordinator.recover();
  environment.visibility(true);
  environment.visibility(false);
  environment.emit('pageshow', { persisted: true });
  environment.network(false);
  environment.network(true);
  await settle(environment, 200);
  assert.equal(coordinator.state.state, 'closed');
  assert.equal(calls.length, 0);
  assert.equal(environment.listenerCount(), 0);
  assert.equal(environment.clock.timers.size, 0);
});
