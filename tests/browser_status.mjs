/** Real Chromium coverage of shared diagnostics, response redaction and lifecycle races. */
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const source = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
if (!process.env.FNPACKKIT_PLAYWRIGHT || !process.env.FNPACKKIT_CHROMIUM) {
    throw new Error('FNPACKKIT_PLAYWRIGHT and FNPACKKIT_CHROMIUM must name existing installations');
}
const { chromium } = await import(pathToFileURL(process.env.FNPACKKIT_PLAYWRIGHT).href);
const evidence = process.env.FNPACKKIT_EVIDENCE || await fs.mkdtemp(path.join(os.tmpdir(), 'fnpackkit-browser-'));
await fs.mkdir(evidence, { recursive: true });
const script = await fs.readFile(path.join(source, 'web/gateway-status.js'));
const style = await fs.readFile(path.join(source, 'web/gateway-status.css'));
let mode = 'rejected';
let requests = 0;
let delayed = null;
let incoming = null;
const server = http.createServer((request, response) => {
    if (request.url === '/status.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript' }); response.end(script); return;
    }
    if (request.url === '/status.css') {
        response.writeHead(200, { 'Content-Type': 'text/css' }); response.end(style); return;
    }
    if (request.url === '/_gateway/status') {
        requests += 1;
        incoming?.(); incoming = null;
        assert.equal(request.method, 'POST');
        assert.ok(request.headers.origin);
        if (mode === 'delay') { delayed = response; return; }
        if (mode === 'html') {
            response.writeHead(403, { 'Content-Type': 'text/html' }); response.end('<b>SECRET-BODY</b>'); return;
        }
        if (mode === 'large') {
            response.writeHead(503, { 'Content-Type': 'application/json' }); response.end('x'.repeat(5000)); return;
        }
        response.writeHead(mode === 'ready' ? 200 : 403, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify(mode === 'ready' ? { schema: 1, status: 'ready' } : {
            schema: 1, status: 'rejected', code: 'origin_mismatch', request_id: 'a'.repeat(32)
        })); return;
    }
    response.writeHead(200, { 'Content-Type': 'text/html' });
    response.end('<!doctype html><link rel="stylesheet" href="/status.css"><script src="/status.js"></script><main>Fixture</main>');
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
let browser;
const checks = [];
try {
    browser = await chromium.launch({ executablePath: process.env.FNPACKKIT_CHROMIUM, headless: true });
    const page = await browser.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}/`);
    await page.evaluate(() => {
        window.retries = 0;
        window.labels = {
            connecting: 'connecting', waiting: 'waiting', failed: 'failed', checking: 'checking',
            http_ready: 'HTTP entry ready; transport still failed', unavailable: 'diagnostic unavailable',
            rejected: 'request rejected', request_id: 'request ID', http_status: 'HTTP status', retry: 'retry',
            reasons: { origin_mismatch: 'origin mismatch' }
        };
        window.widget = window.FnPackKitConnectionStatus.create({
            endpoint: '/_gateway/status', labels: window.labels, timeoutMs: 1000,
            onRetry() { window.retries += 1; }
        });
        window.widget.connecting();
    });
    const panel = page.locator('.fnpackkit-connection-status');
    assert.equal(await panel.getAttribute('data-state'), 'connecting');
    await page.evaluate(() => widget.connected());
    assert.equal(await panel.innerText(), 'waiting');
    await page.evaluate(() => widget.output());
    assert.equal(await panel.isVisible(), false);
    checks.push('connection waits for first output');

    await page.evaluate(() => widget.failed());
    assert.match(await panel.innerText(), /origin mismatch/);
    const original = await panel.innerText();
    const count = requests;
    await page.evaluate(() => widget.failed());
    assert.equal(requests, count);
    assert.equal(await panel.innerText(), original);
    await panel.locator('button').click();
    assert.equal(await page.evaluate(() => retries), 1);
    await page.evaluate(() => widget.output());
    await page.evaluate(() => widget.failed());
    assert.equal(requests, count + 1);
    checks.push('one probe per failure and recovery permits a new probe; explicit retry');

    mode = 'delay';
    await page.evaluate(() => widget.connecting());
    const received = new Promise(resolve => { incoming = resolve; });
    await page.evaluate(() => { window.pendingProbe = widget.failed(); });
    await received;
    await page.evaluate(() => widget.connected());
    delayed.writeHead(403, { 'Content-Type': 'application/json' });
    delayed.end(JSON.stringify({ schema: 1, status: 'rejected', code: 'origin_mismatch' }));
    await page.evaluate(() => pendingProbe);
    assert.equal(await panel.innerText(), 'waiting');
    checks.push('stale probe cannot overwrite reconnected waiting state');

    for (const [responseMode, expected] of [['html', /HTTP status 403/], ['large', /HTTP status 503/], ['ready', /HTTP entry ready; transport still failed/]]) {
        mode = responseMode;
        await page.evaluate(() => widget.connecting());
        await page.evaluate(() => widget.failed());
        assert.match(await panel.innerText(), expected);
        assert.ok(!(await panel.innerText()).includes('SECRET-BODY'));
        checks.push(responseMode + ' response is bounded and accurately classified');
    }
    await page.screenshot({ path: path.join(evidence, 'gateway-status.png') });
    await page.evaluate(() => widget.close());
    assert.equal(await panel.count(), 0);
    await fs.writeFile(path.join(evidence, 'verified.json'), JSON.stringify({ checks, requests }, null, 2) + '\n');
    console.log(evidence);
} finally {
    if (delayed && !delayed.writableEnded) delayed.end();
    await browser?.close();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
}
