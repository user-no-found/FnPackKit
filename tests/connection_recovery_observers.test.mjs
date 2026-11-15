import test from 'node:test';
import assert from 'node:assert/strict';
import { browser, timing, settle, deferred, flush } from './connection_recovery_harness.mjs';

for (const request of ['manual', 'observer']) {
  test(`one ${request} retry after a timeout waits for drain and starts a fresh budget`, async t => {
    const environment = await browser();
    const pending = deferred();
    const contexts = [];
    let running = 0, maximum = 0, syncs = 0, retried = false, coordinator;
    coordinator = environment.create({
      async reconnect(context) {
        contexts.push(context);
        running += 1;
        maximum = Math.max(maximum, running);
        try { if (contexts.length === 1) await pending.promise; }
        finally { running -= 1; }
      },
      resync() { syncs += 1; },
      timing: { ...timing, maxAttempts: 1 },
      onState(snapshot) {
        if (request === 'observer' && snapshot.state === 'failed' && !retried) {
          retried = true;
          coordinator.recover();
        }
      },
    });
    t.after(() => coordinator.close());
    coordinator.recover();
    await settle(environment);
    await settle(environment, timing.attemptTimeoutMs);
    if (request === 'manual') coordinator.recover();
    await settle(environment, 100);
    assert.equal(contexts.length, 1);
    assert.equal(syncs, 0);
    pending.resolve();
    await flush();
    await settle(environment, 50);
    assert.equal(contexts.length, 2);
    assert.deepEqual(contexts.map(context => context.attempt), [1, 1]);
    assert.equal(maximum, 1);
    assert.equal(syncs, 1);
    assert.equal(coordinator.state.state, 'ready');
  });
}

for (const phase of ['checking', 'reconnecting', 'resyncing']) {
  test(`closing from ${phase} observer prevents that business callback and later stages`, async () => {
    const environment = await browser();
    const calls = [], states = [];
    let coordinator;
    coordinator = environment.create({
      check() { calls.push('checking'); return false; },
      reconnect() { calls.push('reconnecting'); },
      resync() { calls.push('resyncing'); },
      timing,
      onState(snapshot) {
        states.push(snapshot.state);
        if (snapshot.state === phase) coordinator.close();
      },
    });
    coordinator.recover();
    await settle(environment, 150);
    assert.deepEqual(calls, ['checking', 'reconnecting', 'resyncing'].slice(0,
      ['checking', 'reconnecting', 'resyncing'].indexOf(phase)));
    assert.equal(coordinator.state.state, 'closed');
    assert.equal(states.includes('ready'), false);
    assert.equal(environment.listenerCount(), 0);
    assert.equal(environment.clock.timers.size, 0);
  });
}

for (const phase of ['checking', 'reconnecting', 'resyncing']) {
  test(`hiding from ${phase} observer fences that callback and restarts only after return`, async t => {
    const environment = await browser();
    const calls = [], states = [];
    let interrupted = false;
    const phases = ['checking', 'reconnecting', 'resyncing'];
    const coordinator = environment.create({
      check() { calls.push('checking'); return false; },
      reconnect() { calls.push('reconnecting'); },
      resync() { calls.push('resyncing'); }, timing,
      onState(snapshot) {
        states.push(snapshot.state);
        if (snapshot.state === phase && !interrupted) {
          interrupted = true;
          environment.visibility(true);
        }
      },
    });
    t.after(() => coordinator.close());
    coordinator.recover();
    await settle(environment, 100);
    assert.deepEqual(calls, phases.slice(0, phases.indexOf(phase)));
    assert.equal(coordinator.state.state, 'waiting');
    assert.equal(states.includes('ready'), false);
    environment.visibility(false);
    await settle(environment);
    assert.deepEqual(calls, [...phases.slice(0, phases.indexOf(phase)), ...phases]);
    assert.equal(coordinator.state.state, 'ready');
  });
}

test('late rejection after timeout and multiple interruptions drains before a new generation', async t => {
  const environment = await browser();
  const pending = deferred();
  const contexts = [], synchronized = [], states = [];
  let running = 0, maximum = 0;
  const coordinator = environment.create({
    async reconnect(context) {
      contexts.push(context);
      running += 1;
      maximum = Math.max(maximum, running);
      try { if (contexts.length === 1) await pending.promise; }
      finally { running -= 1; }
    },
    resync(context) { synchronized.push(context); },
    onState(snapshot) { states.push(snapshot.state); },
    timing: { ...timing, maxAttempts: 1 },
  });
  t.after(() => coordinator.close());
  coordinator.recover();
  await settle(environment);
  await settle(environment, timing.attemptTimeoutMs);
  assert.equal(coordinator.state.code, 'timeout');
  coordinator.recover();
  environment.visibility(true);
  environment.visibility(false);
  environment.network(false);
  environment.network(true);
  await settle(environment, 150);
  assert.equal(contexts.length, 1);
  assert.equal(synchronized.length, 0);
  assert.equal(states.includes('ready'), false);
  pending.reject(new Error('late callback failure'));
  await flush();
  await settle(environment, 50);
  assert.equal(contexts.length, 2);
  assert.equal(maximum, 1);
  assert.equal(synchronized.length, 1);
  assert.notEqual(synchronized[0].signal, contexts[0].signal);
  assert.equal(coordinator.state.state, 'ready');
  assert.equal(environment.logs.length, 0);
});
