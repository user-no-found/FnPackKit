import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

const runtime = process.env.BUILDFLOW_BROWSER_RUNTIME;
const address = process.env.FNPACKKIT_BRIDGE_URL;
if (!runtime || !address) throw new Error('registered browser runtime and live bridge fixture are required');
const { withBrowser } = await import(pathToFileURL(runtime).href);
const prefix = '/app/bridge-test';
const checks = [];
const pageErrors = [];

await withBrowser({}, async browser => {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  page.on('pageerror', error => pageErrors.push(error.message));
  await page.goto(address + '/desktop');
  const window = page.frameLocator('#app');
  await window.getByRole('heading', { name: '界面 v1' }).waitFor();
  const frame = page.frames().find(item => item.url().includes(prefix));
  assert(frame, 'application is rendered inside the desktop iframe');
  const toggle = window.getByRole('button', { name: '后端控制', exact: true });
  await toggle.waitFor();
  const openPanel = async () => {
    // The right-docked button's center is clipped by the iframe; enter its visible half first.
    await toggle.hover({ position: { x: 8, y: 22 } });
    await toggle.click();
  };
  const api = () => frame.evaluate(async path => (await fetch(path + '/api/version')).json(), prefix);
  const counters = async () => (await page.request.get(address + '/__fixture/counters')).json();
  assert.equal((await api()).version, 'v1');
  checks.push('external frontend and API render inside desktop iframe');

  await openPanel();
  await window.getByText('运行中', { exact: true }).waitFor();
  await window.getByRole('button', { name: '应用设置', exact: true }).click();
  await window.locator('#settings').waitFor({ state: 'visible' });
  checks.push('existing floating panel retains application extensions');

  assert.equal((await page.request.post(address + '/__fixture/release')).status(), 204);
  await frame.goto(address + prefix + '/');
  await window.getByRole('heading', { name: '界面 v2' }).waitFor();
  assert.equal((await api()).version, 'v1');
  assert.equal((await counters()).starts, 1);
  checks.push('frontend files update without replacing bridge or restarting backend');

  const echoed = await frame.evaluate(path => new Promise((resolve, reject) => {
    const url = new URL(path + '/hmr', location.href);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(url);
    const timeout = setTimeout(() => { socket.close(); reject(new Error('websocket timed out')); }, 5000);
    socket.onopen = () => socket.send('live-update');
    socket.onmessage = event => { clearTimeout(timeout); socket.close(); resolve(event.data); };
    socket.onerror = () => { clearTimeout(timeout); reject(new Error('websocket failed')); };
  }), prefix);
  assert.equal(echoed, 'live-update');
  checks.push('browser WebSocket uses the same public application prefix');

  await openPanel();
  await window.getByRole('button', { name: '停止后端', exact: true }).click();
  await window.getByRole('button', { name: '取消', exact: true }).click();
  assert.equal((await counters()).stops, 0);
  checks.push('cancel does not stop business service');
  await window.getByRole('button', { name: '停止后端', exact: true }).click();
  await Promise.all([
    frame.waitForNavigation({ waitUntil: 'load' }),
    window.getByRole('button', { name: '确认停止', exact: true }).click(),
  ]);
  await window.getByText('已手动停止', { exact: true }).waitFor();
  const paused = await counters();
  assert.equal(paused.stops, 1);
  await frame.goto(address + prefix + '/');
  await window.getByText('已手动停止', { exact: true }).waitFor();
  assert.equal((await counters()).acquisitions, paused.acquisitions);
  const stoppedAPI = await frame.evaluate(async path => (await fetch(path + '/api/version')).status, prefix);
  assert.equal(stoppedAPI, 503);
  checks.push('actual HTTP service stops and reload/API requests remain paused');

  await Promise.all([
    frame.waitForNavigation({ waitUntil: 'load' }),
    window.getByRole('button', { name: '启动后端', exact: true }).click(),
  ]);
  await window.getByRole('heading', { name: '界面 v2' }).waitFor();
  assert.equal((await api()).version, 'v2');
  assert.equal((await counters()).starts, 2);
  checks.push('start acquires a new endpoint running the updated backend release');
  await openPanel();
  await window.getByRole('button', { name: '重启后端', exact: true }).click();
  await Promise.all([
    frame.waitForNavigation({ waitUntil: 'load' }),
    window.getByRole('button', { name: '确认重启', exact: true }).click(),
  ]);
  await window.getByRole('heading', { name: '界面 v2' }).waitFor();
  assert.equal((await api()).version, 'v2');
  const restarted = await counters();
  assert.equal(restarted.starts, 3);
  assert.equal(restarted.stops, 2);
  checks.push('restart controls the external service through the same floating panel');
  assert.deepEqual(pageErrors, []);
  await page.close();
});

console.log(JSON.stringify({ checks, pageErrors: pageErrors.length }));
