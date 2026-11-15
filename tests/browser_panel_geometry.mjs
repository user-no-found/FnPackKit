import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { controlButton, namedButton, clickToggle, openControls, closeControls, stateIs,
  uiState, mouseDrag, touchDrag, recordPointers, assertHalfGeometry } from './browser_panel_support.mjs';

async function panelFits(page, bounds) {
  await page.waitForFunction(bounds => {
    const panel = document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('.panel');
    const rect = panel.getBoundingClientRect();
    return rect.left >= bounds.left && rect.top >= bounds.top &&
      rect.right <= bounds.right && rect.bottom <= bounds.bottom;
  }, bounds);
  const panel = page.getByRole('dialog');
  assert.equal(await panel.getAttribute('aria-modal'), 'false', 'phone and desktop use the same nonmodal float');
  assert((await panel.boundingBox()).width <= 320, 'compact controls must not become a full-width sheet');
}

export async function verifyPointerGeometry(page, fixture, checks, evidence) {
  await recordPointers(page);
  await page.clock.install();
  await page.clock.pauseAt(new Date(Date.now() + 100));
  for (const [edge, x, y] of [['left', 2, 300], ['right', 998, 300], ['top', 500, 2], ['bottom', 600, 758]]) {
    await mouseDrag(page, x, y);
    await page.clock.runFor(5000); await stateIs(page, 'half');
    await assertHalfGeometry(page, edge);
    if (evidence && edge === 'left') await page.screenshot({ path: resolve(evidence, 'runtime-edge-left.png') });
  }
  const saved = await page.evaluate(prefix => localStorage.getItem('fnpackkit-runtime-position:' + prefix), fixture.prefix);
  assert.equal(JSON.parse(saved).edge, 'bottom');
  // A fresh document independently restores the persisted normalized position.
  await page.clock.resume(); await page.reload(); await controlButton(page).waitFor();
  assert.equal(await controlButton(page).getAttribute('data-edge'), 'bottom');
  const savedAfter = await page.evaluate(prefix => localStorage.getItem('fnpackkit-runtime-position:' + prefix), fixture.prefix);
  assert.equal(savedAfter, saved);
  checks.push('trusted mouse drag docks on all four edges and persisted position survives reload');

  await openControls(page); await closeControls(page);
  await controlButton(page).focus(); await stateIs(page, 'half');
  await page.keyboard.press('Enter'); await stateIs(page, 'revealed');
  await page.keyboard.press('Enter'); await stateIs(page, 'open');
  await page.keyboard.press('Escape'); await stateIs(page, 'half');
  await openControls(page);
  await page.getByRole('heading', { name: '测试业务', exact: true }).click();
  await stateIs(page, 'half');
  await openControls(page); await closeControls(page);
  checks.push('keyboard Enter reveal then open, Escape/× and desktop outside click close immediately without focus reopening');

  for (const cancel of ['pointercancel', 'lostpointercapture']) {
    await recordPointers(page);
    await mouseDrag(page, 100, 280, cancel);
    const before = await uiState(page);
    await clickToggle(page); await stateIs(page, before === 'half' ? 'revealed' : 'open');
    if (await uiState(page) === 'open') await closeControls(page);
  }
  checks.push('pointercancel and real lost capture never accidentally open; next intentional click remains usable');
}

export async function verifyTouchGeometry(page, fixture, checks, evidence) {
  const toggle = controlButton(page);
  await recordPointers(page);
  await touchDrag(page, 4, 300);
  assert.equal(await toggle.getAttribute('data-edge'), 'left');
  const events = await page.evaluate(() => window.fixturePointerTypes);
  assert(events.some(event => event.type === 'touch' && event.trusted), 'CDP must produce actual trusted touch, not pointerType spoofed mouse');
  await openControls(page, true);
  assert((await page.evaluate(() => window.fixtureClicks)).some(event => event.type === 'touch' && event.trusted),
    'fresh tap after native settling must deliver a real trusted touch click, never a synthesized replay');
  await panelFits(page, { left: 0, top: 0, right: 390, bottom: 844 });
  if (evidence) await page.screenshot({ path: resolve(evidence, 'runtime-mobile-open.png') });
  await closeControls(page, true);
  if (evidence) await page.screenshot({ path: resolve(evidence, 'runtime-mobile-half.png') });
  await touchDrag(page, 350, 400, true);
  const beforeCancelClick = await uiState(page);
  const beforeCancelClicks = await page.evaluate(() => window.fixtureClicks.length);
  await clickToggle(page, true);
  await stateIs(page, beforeCancelClick === 'half' ? 'revealed' : 'open');
  assert.equal(await page.evaluate(() => window.fixtureClicks.length), beforeCancelClicks + 1);
  assert.equal(await page.evaluate(() => window.fixtureClicks.at(-1).trusted), true);
  if (await uiState(page) === 'open') await closeControls(page, true);
  checks.push('trusted CDP touch drag works on phone float; touchCancel does not open or consume the next tap');

  await page.evaluate(() => { document.querySelector('.app-toolbar').hidden = false; });
  const launcher = namedButton(page, '应用控制入口');
  await launcher.waitFor({ state: 'visible' });
  await page.waitForFunction(() => !document.querySelector('.app-control-launcher').disabled);
  await toggle.waitFor({ state: 'hidden' });
  const beforeControls = fixture.controls.length;
  await launcher.tap(); await stateIs(page, 'open');
  await panelFits(page, { left: 0, top: 0, right: 390, bottom: 740 });
  assert.equal(fixture.controls.length, beforeControls, 'toolbar opening sends no backend command');
  assert.equal(await launcher.isVisible(), true, 'application toolbar must not be half hidden');
  assert.equal((await launcher.boundingBox()).y >= 740, true);
  await page.getByRole('heading', { name: '测试业务', exact: true }).tap();
  await stateIs(page, 'half');
  assert.equal(await launcher.isVisible(), true);
  await launcher.tap(); await stateIs(page, 'open');

  await page.evaluate(() => {
    for (let index = 0; index < 3; index++) {
      const target = document.createElement('button'); target.id = 'test-choice-' + index;
      target.setAttribute('aria-pressed', 'false');
      target.onclick = () => target.setAttribute('aria-pressed', String(target.getAttribute('aria-pressed') !== 'true'));
      document.body.append(target);
    }
  });
  const choices = ['组合甲', '组合乙', '组合丙'].map(label => namedButton(page, label));
  for (const choice of choices) {
    await choice.tap(); assert.equal(await choice.getAttribute('aria-pressed'), 'true');
    await stateIs(page, 'open');
  }
  const choiceBounds = await Promise.all(choices.map(choice => choice.boundingBox()));
  assert(choiceBounds.every(rect => Math.abs(rect.width - choiceBounds[0].width) < 1));
  assert(choiceBounds.every(rect => Math.abs(rect.y - choiceBounds[0].y) < 1), 'three-column group stays aligned');
  assert.equal(await namedButton(page, '鼠标专用').count(), 0);
  await page.evaluate(() => { document.querySelector('#test-choice-0').disabled = true; });
  await page.waitForFunction(() => document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('[data-action="choice-0"]').disabled);
  await page.evaluate(() => { document.querySelector('#test-choice-0').disabled = false; });
  await page.waitForFunction(() => !document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('[data-action="choice-0"]').disabled);
  await choices[0].tap(); assert.equal(await choices[0].getAttribute('aria-pressed'), 'false');
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('fnpackkit:notice', { detail: { message: '应用操作提示' } })));
  await page.getByText('应用操作提示', { exact: true }).waitFor();
  checks.push('toolbar direct open avoids keybar; three-column touch controls preserve selected/disabled states and notices');

  await page.evaluate(() => {
    for (const [key, value] of Object.entries({ width: 390, height: 460, offsetTop: 24, offsetLeft: 0 }))
      Object.defineProperty(window.visualViewport, key, { configurable: true, value });
    document.querySelector('.app-toolbar').style.bottom = '360px';
    window.visualViewport.dispatchEvent(new Event('resize'));
    window.dispatchEvent(new Event('resize'));
  });
  await panelFits(page, { left: 0, top: 24, right: 390, bottom: 380 });
  if (evidence) await page.screenshot({ path: resolve(evidence, 'runtime-mobile-keyboard-geometry.png') });
  await closeControls(page, true);
  await page.evaluate(() => {
    for (const key of ['width', 'height', 'offsetTop', 'offsetLeft']) delete window.visualViewport[key];
    document.querySelector('.app-toolbar').style.bottom = '0px';
  });
  await page.setViewportSize({ width: 844, height: 390 });
  await launcher.tap(); await stateIs(page, 'open');
  await panelFits(page, { left: 0, top: 0, right: 844, bottom: 286 });
  await closeControls(page, true);
  await page.setViewportSize({ width: 390, height: 844 });

  for (const style of [{ visibility: 'hidden' }, { transform: 'translateX(-200px)' }]) {
    await page.locator('.app-control-launcher').evaluate((node, style) => Object.assign(node.style, style), style);
    await toggle.waitFor({ state: 'visible' });
    await page.locator('.app-control-launcher').evaluate(node => { node.style.visibility = ''; node.style.transform = ''; });
    await toggle.waitFor({ state: 'hidden' });
  }
  await page.evaluate(() => { document.querySelector('.app-control-launcher').remove(); });
  await toggle.waitFor({ state: 'visible' });
  await touchDrag(page, 190, 800);
  await openControls(page, true); await closeControls(page, true);
  const halfAvoid = await toggle.evaluate(node => {
    const host = node.getRootNode().host, root = node.getRootNode();
    const rect = node.getBoundingClientRect(), toolbar = document.querySelector('.app-toolbar');
    const bar = toolbar.getBoundingClientRect(), x = rect.left + rect.width / 2;
    const visibleHit = root.elementFromPoint(x, bar.top - 11);
    const clippedHit = root.elementFromPoint(x, bar.top + 11);
    const toolbarHit = document.elementFromPoint(x, bar.top + 11);
    return { clip: getComputedStyle(node).clipPath, crossesAvoid: rect.bottom > bar.top,
      visibleHit: visibleHit === node || node.contains(visibleHit),
      clippedHit: clippedHit === node || node.contains(clippedHit),
      toolbarHit: toolbarHit !== host && (toolbarHit === toolbar || toolbar.contains(toolbarHit)) };
  });
  assert.notEqual(halfAvoid.clip, 'none');
  assert.equal(halfAvoid.crossesAvoid, true, 'exercise a half launcher at an internal avoid edge');
  assert.equal(halfAvoid.visibleHit, true); assert.equal(halfAvoid.clippedHit, false);
  assert.equal(halfAvoid.toolbarHit, true, 'hidden half must never paint/hit over the application toolbar');
  if (evidence) await page.screenshot({ path: resolve(evidence, 'runtime-mobile-toolbar-half.png') });
  checks.push('half launcher clips its outside half at the internal toolbar edge and preserves toolbar hit targets');
  await page.evaluate(() => { document.querySelector('.app-toolbar').remove(); });
  await toggle.waitFor({ state: 'visible' });
  await page.waitForFunction(() => {
    const rect = document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('.toggle').getBoundingClientRect();
    return Math.abs(rect.top + rect.height / 2 - innerHeight) < 1;
  });
  await openControls(page, true); await closeControls(page, true);

  await page.evaluate(() => {
    navigator.virtualKeyboard.boundingRect = { top: 430, height: 414 };
    navigator.virtualKeyboard.dispatchEvent(new Event('geometrychange'));
  });
  await page.waitForFunction(() => {
    const rect = document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('.toggle').getBoundingClientRect();
    return Math.abs(rect.top + rect.height / 2 - 430) < 1;
  });
  await openControls(page, true);
  await panelFits(page, { left: 0, top: 0, right: 390, bottom: 430 });
  await closeControls(page, true);
  const rect = await toggle.boundingBox();
  assert(rect.y >= 0 && rect.y < 430 && rect.x < 390 && rect.x + rect.width > 0, 'half launcher remains reachable above synthetic keyboard');
  checks.push('portrait/landscape, visualViewport and VirtualKeyboard occlusion keep the same small float reachable; absent/hidden/offscreen launcher restores shared ball');
}
