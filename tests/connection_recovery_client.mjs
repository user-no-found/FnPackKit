const content = document.querySelector('#content');
const metrics = { probes: 0, probeFailures: 0, reconnects: 0, resyncs: 0, received: 0, ticks: 0 };
const probes = new Map();
let socket, hidden = false;
Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
Object.defineProperty(document, 'visibilityState', {
  configurable: true, get: () => hidden ? 'hidden' : 'visible',
});

async function connect({ signal }) {
  signal.throwIfAborted();
  socket?.close();
  const candidate = new WebSocket(location.origin.replace(/^http/, 'ws') + '/events');
  await new Promise((resolve, reject) => {
    const cleanup = () => {
      signal.removeEventListener('abort', aborted);
      candidate.removeEventListener('open', opened);
      candidate.removeEventListener('error', failed);
    };
    const opened = () => { cleanup(); resolve(); };
    const failed = () => { cleanup(); reject(new Error('fixture connection unavailable')); };
    const aborted = () => { cleanup(); candidate.close(); reject(signal.reason); };
    signal.addEventListener('abort', aborted, { once: true });
    candidate.addEventListener('open', opened, { once: true });
    candidate.addEventListener('error', failed, { once: true });
    candidate.addEventListener('message', event => {
      metrics.received += 1;
      const record = JSON.parse(event.data);
      if (record.type === 'ack') probes.get(record.nonce)?.(true);
      if (record.type === 'event') content.textContent = String(record.version);
    });
  });
  signal.throwIfAborted();
  socket = candidate;
}

function check({ signal }) {
  signal.throwIfAborted();
  metrics.probes += 1;
  if (socket?.readyState !== WebSocket.OPEN) { metrics.probeFailures += 1; return false; }
  return new Promise((resolve, reject) => {
    const nonce = String(metrics.probes);
    let timer;
    const cleanup = () => {
      clearTimeout(timer); probes.delete(nonce); signal.removeEventListener('abort', aborted);
    };
    const finish = value => { cleanup(); if (!value) metrics.probeFailures += 1; resolve(value); };
    const aborted = () => { cleanup(); reject(signal.reason); };
    probes.set(nonce, finish);
    signal.addEventListener('abort', aborted, { once: true });
    timer = setTimeout(() => finish(false), 120);
    socket.send(JSON.stringify({ type: 'probe', nonce }));
  });
}

async function resync({ signal }) {
  const response = await fetch('/snapshot', { signal, cache: 'no-store' });
  if (!response.ok) throw new Error('fixture snapshot unavailable');
  const record = await response.json();
  signal.throwIfAborted();
  content.textContent = String(record.version);
  metrics.resyncs += 1;
}

await connect({ signal: new AbortController().signal });
await resync({ signal: new AbortController().signal });
const states = [];
const recovery = window.FnPackKitConnectionRecovery.create({
  check,
  reconnect: async context => { metrics.reconnects += 1; await connect(context); },
  resync,
  onState: snapshot => { states.push(snapshot); },
  timing: {
    debounceMs: 20, attemptTimeoutMs: 2000, retryBaseMs: 20, retryMaxMs: 100,
    maxAttempts: 3, watchIntervalMs: 100, suspendGapMs: 2000,
  },
});
const ticker = setInterval(() => {
  metrics.ticks += 1;
  document.querySelector('#local-clock').textContent = String(metrics.ticks);
}, 20);
document.querySelector('#write').addEventListener('click', async () => {
  const response = await fetch('/action', { method: 'POST', body: '' });
  document.querySelector('#write').dataset.done = String(response.ok);
});
window.fixture = {
  recovery, metrics, states, identity: crypto.randomUUID(),
  get socketState() { return socket.readyState; },
  visibility(value) { hidden = value; document.dispatchEvent(new Event('visibilitychange')); },
  dispose() { recovery.close(); socket.close(); clearInterval(ticker); },
};
window.fixtureReady = true;
