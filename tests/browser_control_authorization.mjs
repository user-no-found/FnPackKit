import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { verifyPanelCredentials } from './browser_panel_credentials.mjs';

const runtime = process.env.BUILDFLOW_BROWSER_RUNTIME;
if (!runtime) throw new Error('registered BuildFlow browser runtime is required');
const { withBrowser } = await import(pathToFileURL(runtime).href);
const prefix = '/app/authorization-test';
const assets = new URL('../runtime/runtimepanel/web/', import.meta.url);
const secret = 'FIXTURE-SUDO-PASSWORD';
const wrongSecret = 'WRONG-FIXTURE-PASSWORD';
const assetNames = new Set([
  'bootstrap.js', 'panel.js', 'source.js', 'extensions.js', 'drag.js',
  'presentation.js', 'viewport.js', 'panel.css',
]);
const errors = [];
let state;

function challenge(password) {
  return {
    schema: 1, state: 'unavailable', paused: false, failed: true,
    problem: {
      code: password ? 'authentication_failed' : 'connection_required',
      message: password ? '授权未通过，请重新输入。' : '尚未连接本地管理服务。请授权后继续。',
      password_required: true,
    },
  };
}

const fixture = {
  prefix, secret, wrongSecret,
  reset() {
    assert(!state?.deferred, 'previous deferred response must be released');
    state = { connected: false, operations: 0, queries: 0, posts: [], deferred: null };
  },
  get posts() { return state.posts; },
  get operations() { return state.operations; },
  get queries() { return state.queries; },
  deferNext(outcome) {
    assert(!state.deferred);
    let received;
    let release;
    const arrival = new Promise(resolve => { received = resolve; });
    const response = new Promise(resolve => { release = resolve; });
    const deferred = { outcome, received, response, release, claimed: false };
    state.deferred = deferred;
    return {
      received: arrival,
      release() {
        assert.equal(state.deferred, deferred);
        state.deferred = null;
        release();
      },
    };
  },
};
fixture.reset();

async function control(req, res) {
  state.queries++;
  let command;
  if (req.method === 'POST') {
    assert.equal(req.headers['x-fnpackkit-control'], '1');
    assert(req.headers.origin);
    let body = '';
    for await (const chunk of req) body += chunk;
    command = JSON.parse(body);
    assert.deepEqual(Object.keys(command).sort(), command.password
      ? ['action', 'confirm', 'password'] : ['action', 'confirm']);
    if (command.password) assert([secret, wrongSecret].includes(command.password));
    state.posts.push(command);
  } else assert.equal(req.method, 'GET');

  let outcome;
  const deferred = state.deferred;
  if (command && deferred && !deferred.claimed) {
    deferred.claimed = true;
    deferred.received();
    await deferred.response;
    outcome = deferred.outcome;
  }
  let result;
  if (outcome === 'terminal') {
    result = {
      schema: 1, state: 'unavailable', paused: false, failed: true,
      problem: { code: 'permission_denied', message: '夹具拒绝此操作。', password_required: false },
    };
  } else if (outcome === 'challenge') result = challenge(command?.password);
  else {
    if (command?.password === secret) state.connected = true;
    if (state.connected) {
      if (command) state.operations++;
      result = { schema: 1, state: 'running', paused: false };
    } else result = challenge(command?.password);
  }
  res.statusCode = result.failed ? 503 : 200;
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify(result));
}

const server = createServer(async (req, res) => {
  try {
    const route = new URL(req.url, 'http://localhost').pathname;
    assert(!req.url.includes(secret) && !req.url.includes(wrongSecret));
    res.setHeader('Cache-Control', 'no-store');
    if (route === prefix + '/_runtime/control') return await control(req, res);
    if (route === prefix + '/_runtime/manifest') {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({
        name: '授权测试', prefix,
        commands: [{ id: 'update-backend', label: '更新后端', confirm: true }],
      }));
      return;
    }
    if (route.startsWith(prefix + '/_runtime/')) {
      const file = route.slice((prefix + '/_runtime/').length);
      if (!assetNames.has(file)) { res.writeHead(404); res.end(); return; }
      res.setHeader('Content-Type', file.endsWith('.css') ? 'text/css' : 'text/javascript');
      res.end(await readFile(new URL(file, assets)));
      return;
    }
    res.setHeader('Content-Type', 'text/html');
    res.end(`<!doctype html><html><head><meta charset="utf-8">
      <meta name="viewport" content="width=device-width,initial-scale=1">
      <script type="module" src="${prefix}/_runtime/bootstrap.js"></script></head>
      <body><h1>${state.connected ? '已连接夹具' : '连接恢复夹具'}</h1>
      <label>普通搜索<input type="text" id="search" aria-label="普通搜索"></label>
      <div contenteditable="true" role="textbox" aria-label="消息编辑器"></div></body></html>`);
  } catch (error) {
    errors.push(error.message);
    res.writeHead(500);
    res.end('fixture failed');
  }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
fixture.base = 'http://127.0.0.1:' + server.address().port;
try {
  const checks = [];
  await withBrowser({}, async browser => {
    for (const phone of [false, true]) {
      const context = await browser.newContext({
        viewport: phone ? { width: 390, height: 844 } : { width: 1100, height: 800 },
        hasTouch: phone, isMobile: phone,
      });
      try {
        checks.push(await verifyPanelCredentials(context, fixture, phone, errors));
      } finally { await context.close(); }
    }
  });
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ checks, pageErrors: errors.length, nativeAutofillVerified: false }));
} finally {
  if (state.deferred) {
    state.deferred.release();
    state.deferred = null;
  }
  await new Promise(resolve => server.close(resolve));
}
