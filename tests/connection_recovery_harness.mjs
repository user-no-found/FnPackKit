import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

export const timing = Object.freeze({
  debounceMs: 10, attemptTimeoutMs: 50, retryBaseMs: 10, retryMaxMs: 20,
  maxAttempts: 3, watchIntervalMs: 10, suspendGapMs: 100,
});

export async function flush() {
  for (let turn = 0; turn < 30; turn += 1) await Promise.resolve();
}

export function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

class TrackedTarget extends EventTarget {
  listeners = new Map();
  addEventListener(type, listener, options) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(listener);
    super.addEventListener(type, listener, options);
  }
  removeEventListener(type, listener, options) {
    this.listeners.get(type)?.delete(listener);
    super.removeEventListener(type, listener, options);
  }
  get listenerCount() {
    return [...this.listeners.values()].reduce((count, listeners) => count + listeners.size, 0);
  }
}

class Clock {
  now = 1000;
  sequence = 0;
  timers = new Map();
  setTimeout = (callback, delay = 0) => this.add(callback, delay, 0);
  setInterval = (callback, delay = 0) => this.add(callback, delay, Math.max(1, delay));
  clear = id => { this.timers.delete(id); };
  add(callback, delay, interval) {
    const id = ++this.sequence;
    this.timers.set(id, { callback, due: this.now + Math.max(0, delay), interval });
    return id;
  }
  jump(milliseconds) { this.now += milliseconds; }
  async advance(milliseconds = 0) {
    const end = this.now + milliseconds;
    for (let iterations = 0; ; iterations += 1) {
      if (iterations > 10000) throw new Error('fake clock exceeded bounded timer budget');
      const next = [...this.timers.entries()]
        .filter(([, timer]) => timer.due <= end)
        .sort((left, right) => left[1].due - right[1].due || left[0] - right[0])[0];
      if (!next) break;
      const [id, timer] = next;
      this.now = Math.max(this.now, timer.due);
      if (timer.interval) timer.due = this.now + timer.interval;
      else this.timers.delete(id);
      timer.callback();
      await flush();
    }
    this.now = Math.max(this.now, end);
    await flush();
  }
}

export async function browser({ hidden = false, online = true } = {}) {
  const source = await readFile(new URL('../web/connection-recovery.js', import.meta.url), 'utf8');
  const clock = new Clock();
  const window = new TrackedTarget();
  const document = new TrackedTarget();
  document.hidden = hidden;
  Object.defineProperty(document, 'visibilityState', {
    get: () => document.hidden ? 'hidden' : 'visible',
  });
  const navigator = { onLine: online };
  class FakeDate extends Date {
    constructor(...args) { super(...(args.length ? args : [clock.now])); }
    static now() { return clock.now; }
  }
  const logs = [];
  const console = Object.fromEntries(['log', 'warn', 'error', 'info', 'debug'].map(level => [
    level, (...args) => logs.push([level, ...args]),
  ]));
  const globals = {
    window, document, navigator, Date: FakeDate, performance: { now: () => clock.now },
    setTimeout: clock.setTimeout, clearTimeout: clock.clear,
    setInterval: clock.setInterval, clearInterval: clock.clear,
    AbortController, AbortSignal, Event, EventTarget, queueMicrotask, console,
  };
  Object.assign(window, globals);
  vm.runInNewContext(source, globals, { filename: 'connection-recovery.js' });
  const emit = (type, properties = {}, target = window) => {
    const event = new Event(type);
    Object.assign(event, properties);
    target.dispatchEvent(event);
  };
  return {
    clock, window, document, navigator, logs,
    create: options => window.FnPackKitConnectionRecovery.create(options),
    emit,
    visibility(value) { document.hidden = value; emit('visibilitychange', {}, document); },
    network(value) { navigator.onLine = value; emit(value ? 'online' : 'offline'); },
    listenerCount: () => window.listenerCount + document.listenerCount,
  };
}

export async function settle(environment, milliseconds = 15) {
  await environment.clock.advance(milliseconds);
}
