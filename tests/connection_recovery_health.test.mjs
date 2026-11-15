import test from 'node:test';
import assert from 'node:assert/strict';
import { browser, timing, settle, deferred, flush } from './connection_recovery_harness.mjs';

for (const background of ['short-timer-live', 'long-timer-live', 'long-throttled']) {
  test(`healthy ${background} return checks once, retains the channel, and still resyncs`, async t => {
    const environment = await browser();
    const probe = deferred(), repair = deferred();
    const calls = [], states = [];
    const coordinator = environment.create({
      check(context) { calls.push(['check', context]); return probe.promise; },
      reconnect(context) { calls.push(['reconnect', context]); },
      resync(context) { calls.push(['resync', context]); return repair.promise; },
      onState(snapshot) { states.push(snapshot.state); }, timing,
    });
    t.after(() => coordinator.close());
    environment.visibility(true);
    if (background === 'long-throttled') environment.clock.jump(timing.suspendGapMs * 3);
    else await settle(environment, background === 'short-timer-live' ? 20 : timing.suspendGapMs * 3);
    assert.equal(calls.length, 0, 'background scheduling is not a channel health proof or a recovery');
    environment.visibility(false);
    environment.visibility(false);
    environment.emit('pageshow', { persisted: false });
    environment.emit('focus');
    environment.emit('online');
    assert.equal(states.includes('ready'), false);
    await settle(environment);
    assert.deepEqual(calls.map(([phase]) => phase), ['check']);
    assert.equal(coordinator.state.state, 'checking');
    for (let index = 0; index < 3; index += 1) {
      environment.emit('focus');
      environment.visibility(false);
    }
    probe.resolve(true);
    await flush();
    assert.deepEqual(calls.map(([phase]) => phase), ['check', 'resync']);
    assert.equal(coordinator.state.state, 'resyncing');
    assert.equal(states.includes('ready'), false, 'check=true does not skip the resync contract');
    assert.equal(calls[0][1], calls[1][1]);
    assert.equal(Object.isFrozen(calls[0][1]), true);
    repair.resolve();
    await flush();
    assert.equal(coordinator.state.state, 'ready');
    environment.emit('focus');
    environment.visibility(false);
    environment.emit('pageshow', { persisted: false });
    await settle(environment, 30);
    assert.deepEqual(calls.map(([phase]) => phase), ['check', 'resync']);
  });
}

for (const result of ['false', 'throws']) {
  test(`timer-live return with check ${result} takes the existing reconnect/resync path`, async t => {
    const environment = await browser();
    const calls = [];
    const coordinator = environment.create({
      check(context) {
        calls.push(['check', context]);
        if (result === 'throws') throw new Error('fixture channel did not acknowledge');
        return false;
      },
      reconnect(context) { calls.push(['reconnect', context]); },
      resync(context) { calls.push(['resync', context]); }, timing,
    });
    t.after(() => coordinator.close());
    environment.visibility(true);
    await settle(environment, timing.suspendGapMs * 3);
    assert.equal(calls.length, 0);
    environment.visibility(false);
    await settle(environment);
    assert.deepEqual(calls.map(([phase]) => phase), ['check', 'reconnect', 'resync']);
    assert.ok(calls.every(([, context]) => context === calls[0][1]));
    assert.equal(coordinator.state.state, 'ready');
  });
}

for (const interrupt of ['hidden', 'offline', 'pagehide']) {
  test(`${interrupt} invalidates a pending healthy result and holds occupancy until it drains`, async t => {
    const environment = await browser();
    const probe = deferred();
    const contexts = [], syncs = [], states = [];
    let active = 0, maximum = 0, rebuilds = 0;
    const coordinator = environment.create({
      async check(context) {
        contexts.push(context);
        active += 1; maximum = Math.max(maximum, active);
        try { return contexts.length === 1 ? await probe.promise : true; }
        finally { active -= 1; }
      },
      reconnect() { rebuilds += 1; },
      resync(context) { syncs.push(context); },
      onState(snapshot) { states.push(snapshot.state); }, timing,
    });
    t.after(() => coordinator.close());
    environment.visibility(true);
    environment.visibility(false);
    await settle(environment);
    if (interrupt === 'hidden') environment.visibility(true);
    else if (interrupt === 'offline') environment.network(false);
    else environment.emit('pagehide');
    assert.equal(contexts[0].signal.aborted, true);
    if (interrupt === 'hidden') environment.visibility(false);
    else if (interrupt === 'offline') environment.network(true);
    else environment.emit('pageshow', { persisted: true });
    environment.emit('focus');
    coordinator.recover();
    await settle(environment, timing.attemptTimeoutMs * 3);
    assert.equal(contexts.length, 1);
    assert.equal(states.includes('ready'), false);
    assert.equal(syncs.length, 0);
    probe.resolve(true);
    await flush();
    assert.equal(syncs.length, 0, 'the cancelled healthy result cannot reach resync');
    await settle(environment);
    assert.equal(contexts.length, 2);
    assert.equal(maximum, 1);
    assert.equal(rebuilds, 0);
    assert.equal(syncs.length, 1);
    assert.equal(syncs[0], contexts[1]);
    assert.notEqual(contexts[0].signal, contexts[1].signal);
    assert.deepEqual(contexts.map(context => context.attempt), [1, 1]);
    assert.equal(coordinator.state.state, 'ready');
  });
}

test('a timed-out healthy result cannot certify a retry; the next attempt checks again after drain', async t => {
  const environment = await browser();
  const pending = deferred();
  const contexts = [], syncs = [], states = [];
  const coordinator = environment.create({
    check(context) { contexts.push(context); return contexts.length === 1 ? pending.promise : true; },
    reconnect() { assert.fail('a fresh successful check retains the channel'); },
    resync(context) { syncs.push(context); },
    onState(snapshot) { states.push(snapshot.state); }, timing,
  });
  t.after(() => coordinator.close());
  environment.visibility(true);
  environment.visibility(false);
  await settle(environment);
  await settle(environment, timing.attemptTimeoutMs * 2);
  assert.equal(coordinator.state.code, 'timeout');
  assert.equal(contexts[0].signal.aborted, true);
  assert.equal(contexts.length, 1);
  assert.equal(states.includes('ready'), false);
  pending.resolve(true);
  await flush();
  assert.equal(syncs.length, 0);
  await settle(environment, timing.retryBaseMs + 1);
  assert.deepEqual(contexts.map(context => context.attempt), [1, 2]);
  assert.notEqual(contexts[0].signal, contexts[1].signal);
  assert.equal(syncs.length, 1);
  assert.equal(syncs[0], contexts[1]);
  assert.equal(coordinator.state.state, 'ready');
});
