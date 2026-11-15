import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';

function frame(value, opcode = 1) {
  const payload = Buffer.from(typeof value === 'string' ? value : JSON.stringify(value));
  assert.ok(payload.length < 126, 'fixture frames must remain bounded');
  return Buffer.concat([Buffer.from([0x80 | opcode, payload.length]), payload]);
}

function receive(socket, onMessage) {
  let buffered = Buffer.alloc(0);
  socket.on('data', chunk => {
    buffered = Buffer.concat([buffered, chunk]);
    while (buffered.length >= 2) {
      const opcode = buffered[0] & 15;
      const masked = Boolean(buffered[1] & 128);
      let size = buffered[1] & 127, offset = 2;
      if (size === 126) {
        if (buffered.length < 4) return;
        size = buffered.readUInt16BE(2); offset = 4;
      } else if (size === 127) { socket.destroy(); return; }
      if (!masked || size > 4096) { socket.destroy(); return; }
      if (buffered.length < offset + 4 + size) return;
      const mask = buffered.subarray(offset, offset + 4);
      offset += 4;
      const payload = Buffer.from(buffered.subarray(offset, offset + size));
      for (let index = 0; index < size; index += 1) payload[index] ^= mask[index % 4];
      buffered = buffered.subarray(offset + size);
      if (opcode === 8) { socket.end(Buffer.from([0x88, 0])); return; }
      if (opcode === 9) { socket.write(frame(payload.toString(), 10)); continue; }
      if (opcode === 1) onMessage(JSON.parse(payload.toString('utf8')));
    }
  });
}

export async function recoveryServer() {
  const source = await readFile(new URL('../web/connection-recovery.js', import.meta.url));
  const client = await readFile(new URL('./connection_recovery_client.mjs', import.meta.url));
  const clients = new Map(), sockets = new Set(), errors = [];
  let sequence = 0, version = 0, actions = 0;
  const server = createServer(async (request, response) => {
    try {
      response.setHeader('Cache-Control', 'no-store');
      if (request.url === '/recovery.js') {
        response.setHeader('Content-Type', 'text/javascript'); response.end(source); return;
      }
      if (request.url === '/client.mjs') {
        response.setHeader('Content-Type', 'text/javascript'); response.end(client); return;
      }
      if (request.url === '/snapshot') {
        assert.equal(request.method, 'GET');
        response.setHeader('Content-Type', 'application/json');
        response.end(JSON.stringify({ version })); return;
      }
      if (request.url === '/action') {
        assert.equal(request.method, 'POST');
        for await (const _chunk of request) { /* Drain the fixture's bounded empty body. */ }
        actions += 1; response.end('accepted'); return;
      }
      response.setHeader('Content-Type', 'text/html; charset=utf-8');
      response.end('<!doctype html><html><head><meta charset="utf-8">' +
        '<meta name="viewport" content="width=device-width,initial-scale=1">' +
        '<script src="/recovery.js"></script><script type="module" src="/client.mjs"></script>' +
        '</head><body><h1>Isolated recovery fixture</h1><textarea id="draft"></textarea>' +
        '<button id="write">Send one write</button><output id="content">0</output>' +
        '<output id="local-clock">0</output></body></html>');
    } catch (error) {
      errors.push(error.message); response.writeHead(500); response.end('fixture failure');
    }
  });
  server.on('connection', socket => {
    sockets.add(socket); socket.on('close', () => sockets.delete(socket));
  });
  server.on('upgrade', (request, socket) => {
    try {
      assert.equal(request.url, '/events');
      const accept = createHash('sha1').update(request.headers['sec-websocket-key'] +
        '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
      socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n' +
        'Connection: Upgrade\r\nSec-WebSocket-Accept: ' + accept + '\r\n\r\n');
      const id = ++sequence;
      const entry = { socket, muted: false };
      clients.set(id, entry);
      socket.on('error', () => {});
      socket.on('close', () => clients.delete(id));
      receive(socket, record => {
        if (entry.muted) return;
        if (record.type === 'probe') socket.write(frame({ type: 'ack', nonce: record.nonce }));
      });
    } catch (error) { errors.push(error.message); socket.destroy(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return {
    address: 'http://127.0.0.1:' + server.address().port,
    get connections() { return sequence; },
    get actions() { return actions; },
    get errors() { return [...errors]; },
    reset() { version = 0; },
    snapshot(next) { version = next; },
    muteExisting() { for (const entry of clients.values()) entry.muted = true; },
    publish(next) {
      version = next;
      for (const entry of clients.values()) {
        if (!entry.muted && !entry.socket.destroyed) entry.socket.write(frame({ type: 'event', version }));
      }
    },
    async close() {
      for (const socket of sockets) socket.destroy();
      await new Promise(resolve => server.close(resolve));
    },
  };
}
