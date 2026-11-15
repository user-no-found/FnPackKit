import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';

const assets = new URL('../runtime/runtimepanel/web/', import.meta.url);
export const panelAssets = new Set([
  'bootstrap.js', 'panel.js', 'scope.js', 'source.js', 'extensions.js', 'drag.js',
  'presentation.js', 'viewport.js', 'state.js', 'geometry.js', 'panel.css',
]);

export async function servePanelAsset(route, prefix, response) {
  if (!route.startsWith(prefix + '/_runtime/')) return false;
  const file = route.slice((prefix + '/_runtime/').length);
  if (!panelAssets.has(file)) { response.writeHead(404); response.end(); return true; }
  response.setHeader('Content-Type', file.endsWith('.css') ? 'text/css' : 'text/javascript');
  response.end(await readFile(new URL(file, assets)));
  return true;
}

export async function runtimeFixture() {
  const prefix = '/app/panel-test';
  const errors = [];
  const fixture = {
    prefix, running: true, paused: false, starts: 0, stops: 0, queries: 0, controls: [], source: null,
    snapshot() {
      return { schema: 1, state: this.running ? 'running' : this.paused ? 'paused' : 'stopped',
        paused: this.paused, ...(this.source ? { source: this.source } : {}) };
    },
  };
  const server = createServer(async (request, response) => {
    try {
      const route = new URL(request.url, 'http://localhost').pathname;
      response.setHeader('Cache-Control', 'no-store');
      if (route === '/__fixture_idle' && request.method === 'POST') {
        fixture.running = false; fixture.paused = false; response.end('ok'); return;
      }
      if (route === prefix + '/_runtime/control') {
        fixture.queries++;
        if (request.method === 'POST') {
          let body = ''; for await (const chunk of request) body += chunk;
          const command = JSON.parse(body); fixture.controls.push(command);
          assert.equal(request.headers['x-fnpackkit-control'], '1');
          assert.equal(request.headers.origin, fixture.base);
          if (command.action === 'stop' || command.action === 'restart') {
            assert.equal(command.confirm, true);
            fixture.running = false; fixture.paused = true; fixture.stops++;
          }
          if (command.action === 'start' || command.action === 'restart') {
            fixture.running = true; fixture.paused = false; fixture.starts++;
          }
        }
        response.setHeader('Content-Type', 'application/json');
        response.end(JSON.stringify(fixture.snapshot())); return;
      }
      if (route === prefix + '/_runtime/manifest') {
        response.setHeader('Content-Type', 'application/json');
        response.end(JSON.stringify({ name: '测试应用', prefix, actions: [
          { id: 'settings', label: '终端显示设置', selector: '#settings-toggle' },
          ...['甲', '乙', '丙'].map((label, index) => ({ id: 'choice-' + index, label: '组合' + label,
            selector: '#test-choice-' + index, group: 'choices', columns: 3, audience: 'touch', keep_open: true })),
          { id: 'pointer-only', label: '鼠标专用', selector: '#pointer-only', audience: 'pointer', keep_open: true },
        ], layout: { launcher: '.app-control-launcher', avoid: '.app-toolbar' } })); return;
      }
      if (await servePanelAsset(route, prefix, response)) return;
      response.setHeader('Content-Type', 'text/html');
      response.end(`<!doctype html><html lang="zh-CN"${fixture.paused ? ' data-runtime-page' : ''}>
        <head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
        <script type="module" src="${prefix}/_runtime/bootstrap.js"></script></head><body>
        <h1>测试业务</h1><label>业务草稿<input aria-label="业务草稿"></label>
        <button id="settings-toggle" onclick="document.querySelector('#settings').hidden=false">显示设置</button>
        <div id="settings" hidden>终端设置</div><button id="pointer-only">鼠标目标</button>
        <div class="app-toolbar" hidden style="position:fixed;bottom:0;left:0;width:100%;height:104px;background:#ccd">
        <button class="app-control-launcher" disabled onclick="window.dispatchEvent(new Event('fnpackkit:open-controls'))">应用控制入口</button>
        </div></body></html>`);
    } catch (error) {
      errors.push(error.message); response.writeHead(500); response.end('synthetic fixture failed');
    }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fixture.base = 'http://127.0.0.1:' + server.address().port;
  fixture.url = fixture.base + prefix + '/';
  fixture.close = async () => {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    assert.deepEqual(errors, [], 'owned HTTP fixture must not report failures');
  };
  return fixture;
}
