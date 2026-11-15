import assert from 'node:assert/strict';
import { controlButton, namedButton, openControls, closeControls, clickToggle, stateIs,
  uiState, mouseDrag, recordPointers, assertHalfGeometry } from './browser_panel_support.mjs';

async function traceStates(page) {
  await page.evaluate(() => {
    const host = document.querySelector('[data-fnpackkit-runtime]');
    window.fixtureStateTrace = [];
    host.shadowRoot.addEventListener('click', () => { window.fixtureLastClick = Date.now(); }, true);
    new MutationObserver(() => {
      const state = host.dataset.uiState;
      if (window.fixtureStateTrace.at(-1)?.state !== state)
        window.fixtureStateTrace.push({ state, at: Date.now(), click: window.fixtureLastClick });
    }).observe(host, { attributes: true, attributeFilter: ['data-ui-state'] });
  });
}

async function realSample(page, delay) {
  return page.evaluate(delay => new Promise(resolve => setTimeout(() => resolve({
    state: document.querySelector('[data-fnpackkit-runtime]').dataset.uiState,
    elapsed: Date.now() - window.fixtureLastClick,
  }), delay)), delay);
}

export async function verifyRealDeadlines(page, checks, touch = false) {
  await traceStates(page);
  await openControls(page, touch); await closeControls(page, touch);
  await clickToggle(page, touch); await stateIs(page, 'revealed');
  assert.equal(await page.getByRole('dialog').isVisible(), false, 'first half click only reveals');
  await controlButton(page).focus();
  const beforeThree = await realSample(page, 2750);
  assert.equal(beforeThree.state, 'revealed', 'real 3s reveal cannot expire early');
  await stateIs(page, 'half');
  const three = await page.evaluate(() => window.fixtureStateTrace.at(-1));
  assert(three.at - three.click >= 3000, 'real reveal deadline must be at least 3000ms');
  assert(three.at - three.click < 4500, 'real reveal timer must expire without hover/focus escape');
  await assertHalfGeometry(page, await controlButton(page).getAttribute('data-edge'));

  await openControls(page, touch);
  // Focus/hover are not activity; status/lifecycle refresh, DOM and resize do not renew idle.
  await namedButton(page, '关闭控制面板').focus();
  const beforeFive = await realSample(page, 4700);
  assert.equal(beforeFive.state, 'open', 'real 5s open cannot close early');
  await page.evaluate(() => {
    document.body.append(document.createElement('span'));
    window.dispatchEvent(new Event('resize'));
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new CustomEvent('fnpackkit:notice', { detail: { message: '非用户状态通知' } }));
  });
  await stateIs(page, 'half');
  const five = await page.evaluate(() => window.fixtureStateTrace.at(-1));
  assert(five.at - five.click >= 5000, 'real open deadline must be at least 5000ms');
  assert(five.at - five.click < 6500, 'poll/mutation/resize/focus must not postpone idle');
  checks.push(`${touch ? 'touch' : 'mouse'} real browser deadlines: 3s=${three.at - three.click}ms, 5s=${five.at - five.click}ms`);
  return { input: touch ? 'touch' : 'mouse', revealMs: three.at - three.click, openMs: five.at - five.click };
}

async function advance(page, duration) { await page.clock.runFor(duration); }

async function inertRefresh(page) {
  const response = page.waitForResponse(response => response.url().endsWith('/_runtime/control'));
  await page.evaluate(() => {
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('resize'));
    document.body.append(document.createElement('i'));
  });
  await response;
  await advance(page, 32);
}

export async function verifyClockDeadlines(page, fixture, checks) {
  const initialStops = fixture.stops;
  // Playwright's clock runs Chromium timers; these exact boundaries are not device sleeps.
  await page.clock.install();
  await page.clock.pauseAt(new Date(Date.now() + 100));
  await openControls(page);
  await advance(page, 32); // Let the paused browser execute the initial floating-position RAF.
  await closeControls(page);
  await clickToggle(page); await stateIs(page, 'revealed');
  await advance(page, 2999); assert.equal(await uiState(page), 'revealed');
  await advance(page, 1); await stateIs(page, 'half');
  await openControls(page);
  await advance(page, 4999); assert.equal(await uiState(page), 'open');
  await advance(page, 1); await stateIs(page, 'half');

  await openControls(page);
  await advance(page, 4000);
  await namedButton(page, '停止后端').click();
  await advance(page, 4999); assert.equal(await uiState(page), 'open', 'confirmation click renews idle');
  await advance(page, 1); await stateIs(page, 'half');
  assert.equal(fixture.stops, initialStops, 'idle confirmation never sends stop');

  await openControls(page);
  const beforePoll = fixture.queries;
  for (let index = 0; index < 4; index++) { await advance(page, 1000); await inertRefresh(page); }
  assert(fixture.queries > beforePoll, 'actual status fetches must occur during the deadline');
  await advance(page, 871); assert.equal(await uiState(page), 'open');
  await advance(page, 1); await stateIs(page, 'half');

  await openControls(page);
  await advance(page, 32);
  await closeControls(page);
  await recordPointers(page);
  await mouseDrag(page, 12, 230);
  await advance(page, 4999); assert.equal(await uiState(page), 'revealed', 'drag gets ordinary 5s idle');
  await advance(page, 1); await stateIs(page, 'half');
  checks.push('Playwright clock: exact 2999/3000 and 4999/5000 boundaries, confirmation and drag reset, actual status polling is inert');

  for (const lifecycle of ['visibilitychange', 'pagehide']) {
    await openControls(page);
    await page.evaluate(type => {
      Object.defineProperty(document, 'hidden', { configurable: true, value: true });
      if (type === 'visibilitychange') document.dispatchEvent(new Event(type));
      else window.dispatchEvent(new PageTransitionEvent(type, { persisted: true }));
    }, lifecycle);
    // setSystemTime simulates a suspended timer queue, unlike runFor firing timers normally.
    await page.clock.setSystemTime(new Date((await page.evaluate(() => Date.now())) + 6000));
    await page.evaluate(type => {
      delete document.hidden;
      if (type === 'visibilitychange') document.dispatchEvent(new Event(type));
      else window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
    }, lifecycle);
    await stateIs(page, 'half');
    await advance(page, 6000); await stateIs(page, 'half');
    await openControls(page); await advance(page, 4999); await stateIs(page, 'open');
    await advance(page, 1); await stateIs(page, 'half');
  }
  checks.push('suspended hidden/pagehide deadlines expire on foreground; stale timers never reopen or close a new owner early');
}
