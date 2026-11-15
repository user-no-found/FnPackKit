import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { runtimeFixture } from './browser_panel_fixture.mjs';
import { controlButton, namedButton, openControls, stateIs } from './browser_panel_support.mjs';
import { verifyRealDeadlines, verifyClockDeadlines } from './browser_panel_timing.mjs';
import { verifyPointerGeometry, verifyTouchGeometry } from './browser_panel_geometry.mjs';

const runtime = process.env.BUILDFLOW_BROWSER_RUNTIME;
if (!runtime) throw new Error('registered BuildFlow browser runtime is required');
const { withBrowser } = await import(pathToFileURL(runtime).href);
const evidence = process.env.FNPACKKIT_RUNTIME_EVIDENCE;
if (evidence) await mkdir(evidence, { recursive: true });
const fixture = await runtimeFixture();
const checks = [];
const realDeadlines = [];
const pageErrors = [];

async function ready(context) {
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on('pageerror', error => pageErrors.push(error.message));
  await page.goto(fixture.url); await controlButton(page).waitFor();
  await page.waitForFunction(() => Boolean(document.querySelector('[data-fnpackkit-runtime]')?.dataset.uiState));
  return page;
}

async function verifyBusiness(page) {
  await openControls(page); await page.getByText('运行中', { exact: true }).waitFor();
  assert.equal(fixture.starts, 0); checks.push('initial status never starts backend');
  assert.equal(await page.locator('.source-summary').isVisible(), false);
  fixture.source = { workspace: { commit: 'a'.repeat(40), fingerprint: 'b'.repeat(64), dirty: true },
    running: { commit: 'c'.repeat(40), fingerprint: 'd'.repeat(64), dirty: false }, changed: true };
  await namedButton(page, '刷新状态').click();
  await page.getByText('本地代码：aaaaaaaaaaaa · 有本地修改（bbbbbbbb）', { exact: true }).waitFor();
  await page.getByText('运行版本：cccccccccccc', { exact: true }).waitFor();
  await page.getByText('本地代码已变化，需构建并重启。', { exact: true }).waitFor();
  fixture.source = { unavailable: true };
  await namedButton(page, '刷新状态').click();
  await page.getByText('源码状态暂不可读，请稍后刷新。', { exact: true }).waitFor();
  assert.equal(await namedButton(page, '停止后端').isEnabled(), true);
  fixture.source = null; await namedButton(page, '刷新状态').click();
  await page.locator('.source-summary').waitFor({ state: 'hidden' });
  checks.push('workspace/startup versions and dirty fingerprints differ; inspection failure retains controls');
  if (evidence) await page.screenshot({ path: resolve(evidence, 'runtime-panel.png') });
  await namedButton(page, '终端显示设置').click(); await page.locator('#settings').waitFor({ state: 'visible' });
  await stateIs(page, 'half');
  await page.evaluate(() => {
    const previous = document.querySelector('#settings-toggle');
    previous.onclick = () => { document.body.dataset.detachedClicks = 'unexpected'; };
    const replacement = document.createElement('button'); replacement.id = 'settings-toggle';
    replacement.textContent = '新设置入口';
    replacement.onclick = () => {
      document.body.dataset.settingsClicks = String(Number(document.body.dataset.settingsClicks || 0) + 1);
      document.querySelector('#settings').hidden = false;
    };
    previous.replaceWith(replacement); document.querySelector('#settings').hidden = true;
  });
  await page.waitForFunction(() => {
    const target = document.querySelector('#settings-toggle'); return target.hidden && target.style.display === 'none';
  });
  await openControls(page);
  const extension = namedButton(page, '终端显示设置');
  assert.equal(await extension.count(), 1); await extension.click();
  assert.equal(await page.locator('body').getAttribute('data-settings-clicks'), '1');
  assert.equal(await page.locator('body').getAttribute('data-detached-clicks'), null);
  await openControls(page);
  await page.evaluate(() => {
    document.querySelector('#settings').hidden = true;
    window.detachedSettingsTarget = document.querySelector('#settings-toggle'); window.detachedSettingsTarget.remove();
    document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('[data-action="settings"]').click();
  });
  assert.equal(await extension.isDisabled(), true); await stateIs(page, 'open');
  assert.equal(await page.locator('body').getAttribute('data-settings-clicks'), '1');
  await page.evaluate(() => { document.body.append(window.detachedSettingsTarget); delete window.detachedSettingsTarget; });
  await page.waitForFunction(() => {
    const target = document.querySelector('#settings-toggle');
    const action = document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('[data-action="settings"]');
    return target.hidden && target.style.display === 'none' && !action.disabled;
  });
  await extension.click(); assert.equal(await page.locator('body').getAttribute('data-settings-clicks'), '2');
  assert.equal(await page.locator('body').getAttribute('data-detached-clicks'), null);
  checks.push('SPA extension replacement/removal/remount always resolves current target without duplicate entries');

  await openControls(page);
  await namedButton(page, '停止后端').click(); await namedButton(page, '取消').click();
  assert.equal(fixture.stops, 0); checks.push('cancel preserves backend');
  await namedButton(page, '停止后端').click();
  await Promise.all([page.waitForNavigation({ waitUntil: 'load' }), namedButton(page, '确认停止').click()]);
  await page.getByText('已手动停止', { exact: true }).waitFor();
  await page.reload(); await page.getByText('已手动停止', { exact: true }).waitFor();
  assert.equal(fixture.starts, 0); assert.equal(fixture.stops, 1);
  checks.push('confirmed stop and reload stay paused');
  await Promise.all([page.waitForNavigation({ waitUntil: 'load' }), namedButton(page, '启动后端').click()]);
  await controlButton(page).waitFor(); await openControls(page); await page.getByText('运行中', { exact: true }).waitFor();
  assert.equal(fixture.starts, 1); checks.push('explicit start recovers page');
  await namedButton(page, '重启后端').click();
  await Promise.all([page.waitForNavigation({ waitUntil: 'load' }), namedButton(page, '确认重启').click()]);
  await controlButton(page).waitFor(); assert.equal(fixture.starts, 2); assert.equal(fixture.stops, 2);
  checks.push('restart sends confirmed command');
  await page.request.post(fixture.base + '/__fixture_idle');
  await openControls(page); await page.getByText('已停止 · 按需启动', { exact: true }).waitFor();
  assert.equal(await namedButton(page, '停止后端').isEnabled(), true);
  await namedButton(page, '停止后端').click();
  await Promise.all([page.waitForNavigation({ waitUntil: 'load' }), namedButton(page, '确认停止').click()]);
  await page.getByText('已手动停止', { exact: true }).waitFor();
  checks.push('idle backend can also be explicitly paused without demand-start');
}

try {
  await withBrowser({}, async browser => {
    const desktop = await browser.newContext({ viewport: { width: 1000, height: 760 }, reducedMotion: 'reduce' });
    try {
      const business = await ready(desktop);
      try { await verifyBusiness(business); } finally { await business.close(); }
      fixture.running = true; fixture.paused = false;
      const realtime = await ready(desktop);
      try { realDeadlines.push(await verifyRealDeadlines(realtime, checks)); } finally { await realtime.close(); }
      const precise = await ready(desktop);
      try { await verifyClockDeadlines(precise, fixture, checks); } finally { await precise.close(); }
      const pointer = await ready(desktop);
      try { await verifyPointerGeometry(pointer, fixture, checks, evidence); } finally { await pointer.close(); }
    } finally { await desktop.close(); }
    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 },
      isMobile: true, hasTouch: true, reducedMotion: 'reduce' });
    await mobile.addInitScript(() => {
      const keyboard = new EventTarget(); keyboard.boundingRect = { top: 844, height: 0 };
      Object.defineProperty(navigator, 'virtualKeyboard', { configurable: true, value: keyboard });
    });
    try {
      const realtime = await ready(mobile);
      try { realDeadlines.push(await verifyRealDeadlines(realtime, checks, true)); } finally { await realtime.close(); }
      const geometry = await ready(mobile);
      try { await verifyTouchGeometry(geometry, fixture, checks, evidence); } finally { await geometry.close(); }
    } finally { await mobile.close(); }
  });
  assert.deepEqual(pageErrors, []);
  const report = { checks, pageErrors: pageErrors.length, controls: fixture.controls, realDeadlines,
    preciseTiming: 'Playwright Chromium clock; not real-device sleeps',
    touchInput: 'CDP Input.dispatchTouchEvent, trusted touch pointers; final contact held stationary for 10 animation frames before lift and a fresh trusted tap',
    deviceBoundary: 'emulated mobile viewport/input and synthetic keyboard geometry; not fnOS Android hardware' };
  if (evidence) await writeFile(resolve(evidence, 'report.json'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify(report, null, 2));
} finally { await fixture.close(); }
