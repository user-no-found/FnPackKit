import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
import { recoveryServer } from './connection_recovery_server.mjs';

const runtime = process.env.BUILDFLOW_BROWSER_RUNTIME;
if (!runtime) throw new Error('registered BuildFlow browser runtime is required');
const { withBrowser } = await import(pathToFileURL(runtime).href);
const server = await recoveryServer();
const pageErrors = [], checks = [];
try {
  await withBrowser({}, async browser => {
    for (const phone of [false, true]) {
      const context = await browser.newContext({
        viewport: phone ? { width: 390, height: 844 } : { width: 1100, height: 800 },
        hasTouch: phone, isMobile: phone,
      });
      try {
        server.reset();
        const beforeConnections = server.connections, beforeActions = server.actions;
        const page = await context.newPage();
        page.on('pageerror', error => pageErrors.push(error.message));
        await page.goto(server.address);
        await page.waitForFunction(() => window.fixtureReady === true);
        const identity = await page.evaluate(() => fixture.identity);
        assert.equal(await page.evaluate(() => fixture.recovery.state.state), 'idle');
        assert.equal(await page.evaluate(() => fixture.metrics.reconnects), 0);
        assert.equal(server.connections - beforeConnections, 1);

        await page.evaluate(() => window.dispatchEvent(new Event('focus')));
        const initialTicks = await page.evaluate(() => fixture.metrics.ticks);
        await page.waitForFunction(ticks => fixture.metrics.ticks >= ticks + 3, initialTicks);
        assert.equal(await page.evaluate(() => fixture.metrics.probes), 0, 'ordinary focus must not recover');
        await page.locator('#write').click();
        await page.waitForFunction(() => document.querySelector('#write').dataset.done === 'true');
        await page.locator('#draft').fill('Unsaved draft remains across recovery');
        server.publish(1);
        await page.waitForFunction(() => document.querySelector('#content').textContent === '1');

        // Both timer-live and throttled returns must check the actual channel, not rebuild it.
        for (const background of ['short-timer-live', 'long-timer-live', 'long-throttled']) {
          const before = await page.evaluate(() => ({ ...fixture.metrics }));
          await page.evaluate(() => fixture.visibility(true));
          const ticks = await page.evaluate(() => fixture.metrics.ticks);
          const requiredTicks = background === 'long-timer-live' ? 110 : 3;
          await page.waitForFunction(({ ticks, requiredTicks }) => fixture.metrics.ticks >= ticks + requiredTicks,
            { ticks, requiredTicks });
          assert.equal(await page.evaluate(() => fixture.metrics.probes), before.probes);
          await page.evaluate(background => {
            const original = Date.now;
            try {
              if (background === 'long-throttled') {
                const base = original();
                Date.now = () => base + 6000;
              }
              fixture.visibility(false);
              document.dispatchEvent(new Event('visibilitychange'));
              window.dispatchEvent(new Event('focus'));
            } finally { Date.now = original; }
          }, background);
          await page.waitForFunction(before => fixture.recovery.state.state === 'ready' &&
            fixture.metrics.resyncs === before.resyncs + 1, before);
          assert.equal(await page.evaluate(() => fixture.metrics.probes), before.probes + 1,
            background + ' must check exactly once');
          assert.equal(await page.evaluate(() => fixture.metrics.reconnects), before.reconnects);
          assert.equal(server.connections - beforeConnections, 1, background + ' must retain the socket');
          await page.evaluate(() => {
            fixture.visibility(false);
            window.dispatchEvent(new Event('pageshow'));
            window.dispatchEvent(new Event('focus'));
          });
          const after = await page.evaluate(() => fixture.metrics.ticks);
          await page.waitForFunction(ticks => fixture.metrics.ticks >= ticks + 3, after);
          assert.equal(await page.evaluate(() => fixture.metrics.probes), before.probes + 1,
            'already-visible/focus/pageshow notifications must not start another check');
        }

        const beforeStall = await page.evaluate(() => ({ ...fixture.metrics }));
        await page.evaluate(() => fixture.visibility(true));
        server.muteExisting();
        server.publish(2);
        const backgroundTicks = await page.evaluate(() => fixture.metrics.ticks);
        await page.waitForFunction(ticks => fixture.metrics.ticks >= ticks + 3, backgroundTicks);
        assert.equal(await page.evaluate(() => fixture.socketState), 1, 'stalled channel remains WebSocket.OPEN');
        assert.equal(await page.locator('#content').textContent(), '1');
        assert.equal(await page.evaluate(() => fixture.metrics.reconnects), 0, 'hidden page cannot reconnect');
        await page.evaluate(() => {
          // No clock jump: the channel can stall even while all background timers run.
          fixture.visibility(false);
          document.dispatchEvent(new Event('visibilitychange'));
          window.dispatchEvent(new Event('focus'));
        });
        await page.waitForFunction(() => fixture.recovery.state.state === 'ready' &&
          document.querySelector('#content').textContent === '2');
        assert.equal(server.connections - beforeConnections, 2, 'resume must create exactly one new connection');
        assert.equal(await page.evaluate(() => fixture.metrics.reconnects), 1);
        assert.equal(await page.evaluate(() => fixture.metrics.probes), beforeStall.probes + 1);
        assert.equal(await page.evaluate(() => fixture.metrics.probeFailures), beforeStall.probeFailures + 1);
        assert.equal(await page.evaluate(() => fixture.metrics.resyncs), beforeStall.resyncs + 1);
        assert.equal(await page.locator('#draft').inputValue(), 'Unsaved draft remains across recovery');
        assert.equal(await page.evaluate(() => fixture.identity), identity, 'recovery must not reload the page');
        assert.equal(server.actions - beforeActions, 1, 'recovery must not replay a write operation');

        server.snapshot(3);
        await page.evaluate(() => {
          const event = new Event('pageshow');
          Object.defineProperty(event, 'persisted', { value: true });
          window.dispatchEvent(event);
        });
        await page.waitForFunction(() => fixture.recovery.state.state === 'ready' &&
          document.querySelector('#content').textContent === '3');
        assert.equal(server.connections - beforeConnections, 2, 'successful in-channel probe skips rebuilding');
        assert.equal(await page.evaluate(() => fixture.metrics.resyncs), beforeStall.resyncs + 2,
          'healthy channel still needs snapshot repair');
        assert.equal(server.actions - beforeActions, 1);

        const beforeClose = await page.evaluate(() => {
          const result = { ...fixture.metrics };
          fixture.dispose();
          fixture.recovery.close();
          fixture.recovery.recover();
          fixture.visibility(true);
          fixture.visibility(false);
          window.dispatchEvent(new Event('online'));
          return result;
        });
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        assert.equal(await page.evaluate(() => fixture.recovery.state.state), 'closed');
        const afterClose = await page.evaluate(() => ({ ...fixture.metrics }));
        for (const field of ['probes', 'reconnects', 'resyncs']) assert.equal(afterClose[field], beforeClose[field]);
        checks.push({
          phone, simulatedLifecycle: true, openButStalledChannel: true,
          freshConnectionAndSnapshot: true, healthyCheckStillResyncs: true,
          healthyShortAndLongReturnRetainsSocket: true, timerLiveStallChecked: true,
          noReload: true, draftRetained: true, writeNotReplayed: true, closedIsInert: true,
        });
      } finally { await context.close(); }
    }
  });
  assert.deepEqual(pageErrors, []);
  assert.deepEqual(server.errors, []);
  console.log(JSON.stringify({ checks, pageErrors: pageErrors.length, androidDeviceVerified: false }));
} finally {
  await server.close();
}
