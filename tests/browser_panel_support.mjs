import assert from 'node:assert/strict';

export const hostSelector = '[data-fnpackkit-runtime]';
export const controlButton = page => page.getByRole('button', { name: '后端控制', exact: true });
export const namedButton = (page, name) => page.getByRole('button', { name, exact: true });

export async function uiState(page) {
  return page.locator(hostSelector).getAttribute('data-ui-state');
}

export async function stateIs(page, expected) {
  await page.waitForFunction(({ selector, expected }) => {
    const host = document.querySelector(selector);
    return host?.dataset.uiState === expected &&
      host.shadowRoot.querySelector('.toggle').dataset.uiState === expected;
  }, { selector: hostSelector, expected });
  assert.equal(await page.locator(hostSelector).locator('.toggle').getAttribute('aria-expanded'),
    String(expected === 'open'));
  assert.equal(await page.getByRole('dialog').isVisible(), expected === 'open');
}

export async function visiblePoint(locator) {
  return locator.evaluate(node => {
    const rect = node.getBoundingClientRect();
    const view = window.visualViewport;
    const left = Math.max(rect.left, view?.offsetLeft || 0);
    const top = Math.max(rect.top, view?.offsetTop || 0);
    const right = Math.min(rect.right, (view?.offsetLeft || 0) + (view?.width || innerWidth));
    const bottom = Math.min(rect.bottom, (view?.offsetTop || 0) + (view?.height || innerHeight));
    const root = node.getRootNode();
    for (const dy of [0.5, 0.25, 0.75]) for (const dx of [0.5, 0.25, 0.75]) {
      const x = left + (right - left) * dx, y = top + (bottom - top) * dy;
      const hit = root.elementFromPoint(x, y);
      if (hit === node || node.contains(hit)) return { x, y };
    }
    throw new Error('launcher has no reachable painted hit target');
  });
}

export async function clickToggle(page, touch = false) {
  const { x, y } = await visiblePoint(controlButton(page));
  if (touch) await page.touchscreen.tap(x, y);
  else await page.mouse.click(x, y);
}

export async function openControls(page, touch = false) {
  if (await uiState(page) === 'open') return;
  if (await uiState(page) === 'half') {
    await clickToggle(page, touch);
    await stateIs(page, 'revealed');
  }
  await clickToggle(page, touch);
  await stateIs(page, 'open');
}

export async function closeControls(page, touch = false) {
  if (touch) await namedButton(page, '关闭控制面板').tap();
  else await namedButton(page, '关闭控制面板').click();
  await stateIs(page, 'half');
}

export async function mouseDrag(page, x, y, cancel = '') {
  const start = await visiblePoint(controlButton(page));
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  await page.mouse.move(x, y, { steps: 12 });
  if (cancel) {
    await controlButton(page).evaluate((node, type) => {
      const pointerId = window.fixturePointerId;
      if (type === 'lostpointercapture') node.releasePointerCapture(pointerId);
      else node.dispatchEvent(new PointerEvent(type, { pointerId, bubbles: true }));
    }, cancel);
  }
  await page.mouse.up();
  assert.notEqual(await uiState(page), 'open', 'drag/cancel must not synthesize an open click');
  assert.equal(await controlButton(page).getAttribute('data-dragging'), null);
}

export async function touchDrag(page, x, y, cancel = false) {
  const session = await page.context().newCDPSession(page);
  try {
    const start = await visiblePoint(controlButton(page));
    const point = (x, y) => [{ x, y, id: 7, radiusX: 2, radiusY: 2, force: 1 }];
    await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point(start.x, start.y) });
    for (let step = 1; step <= 8; step++) {
      await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints:
        point(start.x + (x - start.x) * step / 8, start.y + (y - start.y) * step / 8) });
    }
    // End a drag with a stationary held finger rather than an instantaneous high-speed release.
    // Native-button controls show this avoids Chromium's moved-touch compatibility-click guard.
    await page.evaluate(() => new Promise(resolve => {
      let remaining = 10;
      const frame = () => { if (--remaining) requestAnimationFrame(frame); else resolve(); };
      requestAnimationFrame(frame);
    }));
    await session.send('Input.dispatchTouchEvent', { type: cancel ? 'touchCancel' : 'touchEnd', touchPoints: [] });
    assert.notEqual(await uiState(page), 'open', 'real CDP touch drag must not open controls');
    assert.equal(await controlButton(page).getAttribute('data-dragging'), null);
  } finally { await session.detach(); }
}

export async function recordPointers(page) {
  await controlButton(page).evaluate(node => {
    window.fixturePointerTypes = [];
    window.fixtureClicks = [];
    node.addEventListener('click', event => window.fixtureClicks.push({
      type: event.pointerType, trusted: event.isTrusted, detail: event.detail,
    }), true);
    node.addEventListener('pointerdown', event => {
      window.fixturePointerId = event.pointerId;
      window.fixturePointerTypes.push({ type: event.pointerType, trusted: event.isTrusted });
    });
  });
}

export async function assertHalfGeometry(page, edge) {
  const rect = await controlButton(page).boundingBox();
  const view = await page.evaluate(() => ({ width: innerWidth, height: innerHeight }));
  assert.equal(await controlButton(page).getAttribute('data-edge'), edge);
  const hidden = edge === 'left' ? -rect.x : edge === 'right' ? rect.x + rect.width - view.width
    : edge === 'top' ? -rect.y : rect.y + rect.height - view.height;
  assert(Math.abs(hidden - 22) <= 1, `${edge} must hide exactly half the 44px launcher: ${hidden}`);
}
