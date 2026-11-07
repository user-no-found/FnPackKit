import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

const runtime = process.env.BUILDFLOW_BROWSER_RUNTIME;
const native = process.env.LOCAL_NATIVE_URL;
const relay = process.env.LOCAL_RELAY_URL;
if (!runtime || !native || !relay) throw new Error('registered browser and independent local fixture required');
const { withBrowser } = await import(pathToFileURL(runtime).href);
const prefix = '/app/local-fixture';
const checks = [];
const pageErrors = [];

await withBrowser({}, async browser => {
  const context = await browser.newContext({ viewport: { width: 1100, height: 850 } });
  await context.addCookies([{ name: 'local_session', value: 'independent-local-fixture', url: native }]);
  const page = await context.newPage();
  page.on('pageerror', error => pageErrors.push(error.message));
  const state = () => page.evaluate(async path => (await fetch(path + '/api/state')).json(), prefix);
  const open = async () => {
    const toggle = page.getByRole('button', { name: '后端控制', exact: true });
    await toggle.hover({ position: { x: 8, y: 22 } });
    if (await toggle.getAttribute('aria-expanded') !== 'true') await toggle.click();
  };
  await page.goto(native + prefix + '/');
  await page.getByRole('heading', { name: '本地界面' }).waitFor();
  const original = await state();
  const saved = await page.evaluate(async path => (await fetch(path + '/api/value', {
    method: 'POST', body: 'browser persistent data',
  })).status, prefix);
  assert.equal(saved, 204);
  checks.push('native frontend, backend and persistent writes work before the FPK entry is used');

  await page.goto(relay + prefix + '/');
  await page.getByRole('heading', { name: '本地界面' }).waitFor();
  assert.equal((await state()).pid, original.pid);
  assert.equal((await state()).saved, 'browser persistent data');
  assert.equal(await page.locator('[data-fnpackkit-runtime]').count(), 1);
  checks.push('FPK shows the same local business process and data with one management panel');

  await open();
  await page.getByRole('dialog', { name: 'FPK 后端管理 后端控制', exact: true }).waitFor();
  await page.getByText('运行中', { exact: true }).waitFor();
  await page.getByRole('button', { name: '停止后端', exact: true }).click();
  await page.getByRole('button', { name: '取消', exact: true }).click();
  assert.equal((await state()).pid, original.pid);
  await page.getByRole('button', { name: '停止后端', exact: true }).click();
  await Promise.all([
    page.waitForNavigation(),
    page.getByRole('button', { name: '确认停止', exact: true }).click(),
  ]);
  await page.getByText('已手动停止', { exact: true }).waitFor();
  const control = await page.evaluate(async path => (await fetch(path + '/_runtime/control')).json(), prefix);
  assert.equal(control.paused, true);
  assert.equal(control.source.running, undefined);
  checks.push('FPK owns the panel and can stop the real backend while local management retains the authoritative state');

  const release = await (await page.request.post(relay + '/__fixture/advance')).json();
  await open();
  await page.getByRole('button', { name: '更新后端', exact: true }).click();
  await Promise.all([
    page.waitForNavigation(),
    page.getByRole('button', { name: '确认更新后端', exact: true }).click(),
  ]);
  await page.getByText('已手动停止', { exact: true }).waitFor();
  checks.push('native update fetches the configured branch without starting a stopped business');
  await Promise.all([
    page.waitForNavigation(),
    page.getByRole('button', { name: '启动后端', exact: true }).click(),
  ]);
  await page.getByRole('heading', { name: '本地界面' }).waitFor();
  const updated = await state();
  assert.equal(updated.version, 'browser updated backend');
  assert.equal(updated.commit, release.commit);
  assert.equal(updated.saved, 'browser persistent data');
  assert.notEqual(updated.pid, original.pid);
  checks.push('Web start returns the new real backend revision and the unchanged data store');

  await open();
  await page.getByRole('button', { name: '重启后端', exact: true }).click();
  await Promise.all([
    page.waitForNavigation(),
    page.getByRole('button', { name: '确认重启', exact: true }).click(),
  ]);
  await page.getByRole('heading', { name: '本地界面' }).waitFor();
  const restarted = await state();
  assert.notEqual(restarted.pid, updated.pid);
  await page.goto(native + prefix + '/');
  assert.equal((await state()).pid, restarted.pid);
  assert.equal((await state()).saved, 'browser persistent data');
  checks.push('native access observes the same application after Web-controlled update and restart');
  assert.deepEqual(pageErrors, []);
  await context.close();
});

console.log(JSON.stringify({ checks, pageErrors: pageErrors.length }));
