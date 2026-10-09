/** Real browser negotiation of one-use grants without framework-specific transport APIs. */
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

if (!process.env.FNPACKKIT_PLAYWRIGHT || !process.env.FNPACKKIT_CHROMIUM) {
    throw new Error('FNPACKKIT_PLAYWRIGHT and FNPACKKIT_CHROMIUM must name existing installations');
}
const { chromium } = await import(pathToFileURL(process.env.FNPACKKIT_PLAYWRIGHT).href);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const script = await fs.readFile(path.join(root, 'web/gateway-transport.js'));
const evidence = process.env.FNPACKKIT_EVIDENCE || await fs.mkdtemp(path.join(os.tmpdir(), 'fnpackkit-transport-'));
await fs.mkdir(evidence, { recursive: true });
const tickets = new Set();
let posts = 0;
let upgrades = 0;
let delayed = null;
let received = null;
let mode = 'normal';
const observed = [];
const marker = 'fnpackkit.gateway.v1';
const prefix = 'fpk-gateway.';
function grant(response) {
    const token = crypto.randomBytes(32).toString('base64url');
    tickets.add(prefix + token);
    response.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
    response.end(JSON.stringify({ schema: 1, status: 'granted', protocol: prefix + token, expires_in_seconds: 30 }));
}
const server = http.createServer((request, response) => {
    if (request.url === '/transport.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript' }); response.end(script); return;
    }
    if (request.url === '/_gateway/grant') {
        posts += 1;
        assert.equal(request.method, 'POST');
        assert.equal(request.headers['sec-fetch-site'], 'same-origin');
        assert.ok(request.headers.origin);
        if (mode === 'delay') { delayed = response; received?.(); return; }
        grant(response); return;
    }
    response.writeHead(200, { 'Content-Type': 'text/html' });
    response.end('<!doctype html><script src="/transport.js"></script><main>Transport fixture</main>');
});
server.on('upgrade', (request, socket) => {
    assert.equal(request.url, '/ws');
    const protocols = request.headers['sec-websocket-protocol'].split(',').map(value => value.trim());
    const token = protocols.find(value => value.startsWith(prefix));
    assert.ok(protocols.includes(marker));
    assert.ok(tickets.delete(token));
    upgrades += 1;
    const selected = protocols.includes('chat') ? 'chat' : marker;
    observed.push({ fetch_site: request.headers['sec-fetch-site'] || 'absent', selected });
    const accept = crypto.createHash('sha1').update(request.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
    socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n' +
        'Sec-WebSocket-Accept: ' + accept + '\r\nSec-WebSocket-Protocol: ' + selected + '\r\n\r\n');
    socket.write(Buffer.concat([Buffer.from([0x81, 5]), Buffer.from('ready')]));
    socket.on('data', () => socket.end());
    socket.on('error', () => {});
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
let browser;
try {
    browser = await chromium.launch({ executablePath: process.env.FNPACKKIT_CHROMIUM, headless: true });
    const page = await browser.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}/`);
    await page.evaluate(() => {
        window.messages = [];
        window.factory = (address, options) => {
            let socket;
            return {
                get socket() { return socket; },
                start(sink) {
                    return new Promise((resolve, reject) => {
                        socket = new WebSocket(address, options.protocols);
                        socket.onopen = resolve;
                        socket.onerror = () => reject(new Error('fixture handshake failed'));
                        socket.onmessage = event => sink.data(event.data);
                    });
                },
                send(data) { socket.send(data); },
                close() { socket?.close(); }
            };
        };
        window.makeTransport = protocols => window.FnPackKitGatewayTransport.create(
            window.factory, 'ws://' + location.host + '/ws', { grantEndpoint: '/_gateway/grant', protocols });
    });
    for (const protocols of [undefined, ['chat'], undefined]) {
        await page.evaluate(async protocols => {
            window.transport = makeTransport(protocols);
            await transport.start({ data: value => messages.push(value) });
        }, protocols);
        await page.waitForFunction(() => messages.length > 0);
        const selected = await page.evaluate(() => transport.socket.protocol);
        assert.equal(selected, protocols ? 'chat' : marker);
        await page.evaluate(() => { transport.close(); messages = []; });
    }
    assert.equal(posts, 3);
    assert.equal(upgrades, 3);
    mode = 'delay';
    const pendingRequest = new Promise(resolve => { received = resolve; });
    await page.evaluate(() => {
        window.transport = makeTransport();
        window.pendingStart = transport.start({ data() {} }).then(() => 'started', () => 'cancelled');
    });
    await pendingRequest;
    await page.evaluate(() => transport.close());
    grant(delayed);
    assert.equal(await page.evaluate(() => pendingStart), 'cancelled');
    assert.equal(upgrades, 3);
    await fs.writeFile(path.join(evidence, 'verified.json'), JSON.stringify({
        posts, upgrades, observed,
        checks: ['real WebSocket marker negotiation', 'application subprotocol preserved',
            'fresh grant on each attempt', 'cancelled grant never opens socket', 'no ticket in URL or response protocol']
    }, null, 2) + '\n');
    console.log(evidence);
} finally {
    if (delayed && !delayed.writableEnded) delayed.end();
    await browser?.close();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
}
