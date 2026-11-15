import test from 'node:test';
import assert from 'node:assert/strict';
import { browser, timing, settle, deferred, flush } from './connection_recovery_harness.mjs';

for (const phase of ['check', 'reconnect', 'resync']) {
  test(`absolute deadline rejects ${phase} completion before its timer can run`, async t => {
    const environment = await browser();
    const calls = [], states = [];
    let activeSignal;
    const callbacks = Object.fromEntries(['check', 'reconnect', 'resync'].map(name => [name, context => {
      calls.push(name); activeSignal = context.signal;
      if (phase === name) environment.clock.jump(timing.attemptTimeoutMs + 1);
      return name === 'check' ? false : undefined;
    }]));
    const coordinator = environment.create({
      ...callbacks, timing: { ...timing, maxAttempts: 1 },
      onState(snapshot) { states.push(snapshot.state); },
    });
    t.after(() => coordinator.close());
    coordinator.recover();
    await settle(environment);
    assert.equal(activeSignal.aborted, true);
    assert.equal(coordinator.state.state, 'failed');
    assert.equal(coordinator.state.code, 'timeout');
    assert.equal(states.includes('ready'), false);
    assert.deepEqual(calls, ['check', 'reconnect', 'resync'].slice(0, ['check', 'reconnect', 'resync'].indexOf(phase) + 1));
  });
}

for (const phase of ['check', 'resync']) {
  test(`${phase} timeout aborts its signal and does not publish late ready`, async t => {
    const environment = await browser();
    const pending = deferred();
    let activeSignal, rebuilds = 0;
    const options = {
      reconnect() { rebuilds += 1; }, resync() {},
      timing: { ...timing, maxAttempts: 1 },
    };
    options[phase] = context => { activeSignal = context.signal; return pending.promise; };
    const coordinator = environment.create(options);
    t.after(() => coordinator.close());
    coordinator.recover();
    await settle(environment);
    await settle(environment, timing.attemptTimeoutMs);
    assert.equal(activeSignal.aborted, true);
    assert.equal(coordinator.state.state, 'failed');
    assert.equal(coordinator.state.code, 'timeout');
    pending.resolve(phase === 'check' ? true : undefined);
    await flush();
    assert.equal(coordinator.state.state, 'failed');
    assert.equal(rebuilds, phase === 'check' ? 0 : 1);
  });
}

test('check must return an actual boolean; invalid results never reach business stages', async t => {
  const environment = await browser();
  let checks = 0, rebuilds = 0, syncs = 0;
  const coordinator = environment.create({
    check() { checks += 1; return 1; },
    reconnect() { rebuilds += 1; }, resync() { syncs += 1; }, timing,
  });
  t.after(() => coordinator.close());
  coordinator.recover();
  await settle(environment, 150);
  assert.equal(checks, timing.maxAttempts);
  assert.equal(rebuilds, 0);
  assert.equal(syncs, 0);
  assert.equal(coordinator.state.state, 'failed');
  assert.equal(coordinator.state.code, 'check_failed');
});

for (const terminal of ['ready', 'failed']) {
  test(`recover inside ${terminal} observer starts a new budget after the old operation drains`, async t => {
    const environment = await browser();
    const contexts = [];
    let requested = false, coordinator;
    coordinator = environment.create({
      reconnect(context) {
        contexts.push(context);
        if (terminal === 'failed' && contexts.length === 1) throw new Error('first attempt rejected');
      },
      resync() {}, timing: { ...timing, maxAttempts: 1 },
      onState(snapshot) {
        if (!requested && snapshot.state === terminal) { requested = true; coordinator.recover(); }
      },
    });
    t.after(() => coordinator.close());
    coordinator.recover();
    await settle(environment, 100);
    assert.equal(contexts.length, 2);
    assert.deepEqual(contexts.map(context => context.attempt), [1, 1]);
    assert.notEqual(contexts[0].signal, contexts[1].signal);
    assert.equal(coordinator.state.state, 'ready');
  });
}

test('isolated online notifications do not interrupt an already online page', async t => {
  const environment = await browser();
  let rebuilds = 0;
  const coordinator = environment.create({ reconnect() { rebuilds += 1; }, resync() {}, timing });
  t.after(() => coordinator.close());
  environment.emit('online');
  await settle(environment);
  assert.equal(rebuilds, 0);
  coordinator.recover();
  await settle(environment);
  assert.equal(rebuilds, 1);
  environment.emit('online');
  environment.emit('online');
  await settle(environment, 50);
  assert.equal(rebuilds, 1);
  environment.network(false);
  environment.network(true);
  await settle(environment);
  assert.equal(rebuilds, 2);
});

test('zero debounce is supported and rejected async observers cannot leak unhandled errors', async t => {
  const environment = await browser();
  let rebuilds = 0;
  const coordinator = environment.create({
    reconnect() { rebuilds += 1; }, resync() {},
    onState: async () => { throw new Error('observer rejection'); },
    timing: { ...timing, debounceMs: 0 },
  });
  t.after(() => coordinator.close());
  coordinator.recover();
  await settle(environment, 0);
  assert.equal(rebuilds, 1);
  assert.equal(coordinator.state.state, 'ready');
  assert.equal(environment.logs.length, 0);
});
