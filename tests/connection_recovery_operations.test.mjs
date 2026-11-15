import test from 'node:test';
import assert from 'node:assert/strict';
import { browser, timing, settle, deferred, flush } from './connection_recovery_harness.mjs';

function attach(t, environment, options) {
  const coordinator = environment.create({ timing, ...options });
  t.after(() => coordinator.close());
  return coordinator;
}

for (const outcome of ['healthy', 'unhealthy', 'throws']) {
  test(`check ${outcome}: recovery always resyncs and rebuilds only when unhealthy`, async t => {
    const environment = await browser();
    const calls = [];
    const coordinator = attach(t, environment, {
      check(context) {
        calls.push(['check', context]);
        if (outcome === 'throws') throw new Error('PRIVATE-CHECK-DETAIL');
        return outcome === 'healthy';
      },
      reconnect(context) { calls.push(['reconnect', context]); },
      resync(context) { calls.push(['resync', context]); },
    });
    coordinator.recover();
    await settle(environment);
    assert.deepEqual(calls.map(([stage]) => stage), outcome === 'healthy'
      ? ['check', 'resync'] : ['check', 'reconnect', 'resync']);
    assert.ok(calls.every(([, context]) => context.signal === calls[0][1].signal));
    assert.equal(coordinator.state.state, 'ready');
    assert.equal(environment.logs.length, 0);
    assert.doesNotMatch(JSON.stringify(coordinator.state), /PRIVATE-CHECK-DETAIL/);
  });
}

test('reconnect failures retry finitely with attempt numbers and then resync', async t => {
  const environment = await browser();
  const attempts = [], states = [], syncs = [];
  const coordinator = attach(t, environment, {
    reconnect(context) {
      attempts.push(context.attempt);
      if (context.attempt < 3) throw new Error('PRIVATE-RECONNECT-DETAIL');
    },
    resync(context) { syncs.push(context.attempt); },
    onState(snapshot) { states.push(snapshot); },
  });
  coordinator.recover();
  await settle(environment, 150);
  assert.deepEqual(attempts, [1, 2, 3]);
  assert.deepEqual(syncs, [3]);
  assert.equal(coordinator.state.state, 'ready');
  assert.ok(states.some(snapshot => snapshot.state === 'retrying'));
  assert.doesNotMatch(JSON.stringify(states), /PRIVATE-RECONNECT-DETAIL/);
  assert.equal(environment.logs.length, 0);
});

test('retry delays increase within the configured cap and stop at maxAttempts', async t => {
  const environment = await browser();
  const starts = [];
  let fail = true;
  const coordinator = attach(t, environment, {
    reconnect(context) {
      starts.push([context.attempt, environment.clock.now]);
      if (fail) throw new Error('fixture failure');
    },
    resync() {},
  });
  coordinator.recover();
  await settle(environment, 150);
  assert.deepEqual(starts.map(([attempt]) => attempt), [1, 2, 3]);
  const delays = starts.slice(1).map(([, start], index) => start - starts[index][1]);
  assert.ok(delays.every(delay => delay > 0 && delay <= timing.retryMaxMs + timing.debounceMs));
  assert.ok(delays[1] >= delays[0]);
  assert.equal(coordinator.state.state, 'failed');
  await settle(environment, 150);
  assert.equal(starts.length, timing.maxAttempts);
  fail = false;
  coordinator.recover();
  await settle(environment);
  assert.equal(starts.at(-1)[0], 1);
  assert.equal(coordinator.state.state, 'ready');
});

test('failed resync retries the recovery pipeline rather than publishing ready', async t => {
  const environment = await browser();
  const calls = [];
  const coordinator = attach(t, environment, {
    reconnect(context) { calls.push(['reconnect', context.attempt]); },
    resync(context) {
      calls.push(['resync', context.attempt]);
      if (context.attempt === 1) throw new Error('snapshot unavailable');
    },
  });
  coordinator.recover();
  await settle(environment, 100);
  assert.deepEqual(calls, [['reconnect', 1], ['resync', 1], ['reconnect', 2], ['resync', 2]]);
  assert.equal(coordinator.state.state, 'ready');
});

test('onState exceptions never change callback results or produce ad-hoc logs', async t => {
  const environment = await browser();
  let syncs = 0;
  const coordinator = attach(t, environment, {
    reconnect() {}, resync() { syncs += 1; },
    onState() { throw new Error('observer failure'); },
  });
  coordinator.recover();
  await settle(environment);
  assert.equal(syncs, 1);
  assert.equal(coordinator.state.state, 'ready');
  assert.equal(environment.logs.length, 0);
});

test('timeout publishes failure promptly but cannot overlap a noncooperative callback', async t => {
  const environment = await browser();
  const first = deferred();
  const contexts = [], syncs = [];
  let active = 0, maximum = 0;
  const coordinator = attach(t, environment, {
    timing: { ...timing, maxAttempts: 1 },
    async reconnect(context) {
      contexts.push(context);
      active += 1; maximum = Math.max(maximum, active);
      try { if (contexts.length === 1) await first.promise; }
      finally { active -= 1; }
    },
    resync(context) { syncs.push(context); },
  });
  coordinator.recover();
  await settle(environment);
  assert.equal(contexts.length, 1);
  await settle(environment, timing.attemptTimeoutMs);
  assert.equal(contexts[0].signal.aborted, true);
  assert.equal(coordinator.state.state, 'failed');
  assert.equal(coordinator.state.code, 'timeout');
  coordinator.recover();
  await settle(environment, 150);
  assert.equal(contexts.length, 1);
  assert.equal(syncs.length, 0);
  first.resolve();
  await flush();
  coordinator.recover();
  await settle(environment, 50);
  assert.equal(maximum, 1);
  assert.ok(syncs.every(context => context.signal !== contexts[0].signal));
  assert.equal(coordinator.state.state, 'ready');
});

test('hiding aborts an in-flight callback and prevents stale resync or readiness', async t => {
  const environment = await browser();
  const pending = deferred();
  const contexts = [], syncs = [], states = [];
  const coordinator = attach(t, environment, {
    reconnect(context) { contexts.push(context); return contexts.length === 1 ? pending.promise : undefined; },
    resync(context) { syncs.push(context); },
    onState(snapshot) { states.push(snapshot.state); },
  });
  coordinator.recover();
  await settle(environment);
  environment.visibility(true);
  assert.equal(contexts[0].signal.aborted, true);
  await settle(environment, 100);
  assert.equal(coordinator.state.state, 'waiting');
  assert.equal(contexts.length, 1);
  pending.resolve();
  await flush();
  assert.equal(syncs.length, 0);
  assert.equal(states.includes('ready'), false);
  environment.visibility(false);
  await settle(environment);
  assert.equal(contexts.length, 2);
  assert.equal(syncs.length, 1);
  assert.notEqual(syncs[0].signal, contexts[0].signal);
  assert.equal(coordinator.state.state, 'ready');
});

test('offline cancellation waits for the old promise before foreground recovery', async t => {
  const environment = await browser();
  const pending = deferred();
  const contexts = [], syncs = [];
  const coordinator = attach(t, environment, {
    reconnect(context) { contexts.push(context); return contexts.length === 1 ? pending.promise : undefined; },
    resync(context) { syncs.push(context); },
  });
  coordinator.recover();
  await settle(environment);
  environment.network(false);
  assert.equal(contexts[0].signal.aborted, true);
  environment.network(true);
  await settle(environment, 100);
  assert.equal(contexts.length, 1);
  pending.resolve();
  await flush();
  await settle(environment, 50);
  assert.equal(contexts.length, 2);
  assert.equal(syncs.length, 1);
  assert.equal(coordinator.state.state, 'ready');
});

test('close cancels resync and a late completion cannot replace closed state', async () => {
  const environment = await browser();
  const pending = deferred();
  let context;
  const states = [];
  const coordinator = environment.create({
    reconnect() {}, resync(value) { context = value; return pending.promise; },
    onState(snapshot) { states.push(snapshot.state); }, timing,
  });
  coordinator.recover();
  await settle(environment);
  assert.equal(coordinator.state.state, 'resyncing');
  coordinator.close();
  assert.equal(context.signal.aborted, true);
  pending.resolve();
  await flush();
  await settle(environment, 100);
  assert.equal(coordinator.state.state, 'closed');
  assert.equal(states.at(-1), 'closed');
  assert.equal(states.includes('ready'), false);
  assert.equal(environment.clock.timers.size, 0);
});
