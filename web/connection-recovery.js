/* Coordinate foreground recovery; applications own their connection and snapshots. */
(function (global) {
    'use strict';
    const defaults = Object.freeze({
        debounceMs: 100, attemptTimeoutMs: 15000, retryBaseMs: 500,
        retryMaxMs: 10000, maxAttempts: 3, watchIntervalMs: 1000, suspendGapMs: 5000
    });
    const timeoutReason = Symbol('recovery deadline');

    function configuration(options) {
        if (!options || typeof options !== 'object' || Array.isArray(options) ||
            Object.keys(options).some(key => !['check', 'reconnect', 'resync', 'onState', 'timing'].includes(key))) {
            throw new TypeError('connection recovery requires callback options');
        }
        for (const name of ['reconnect', 'resync']) {
            if (typeof options[name] !== 'function') throw new TypeError(name + ' callback is required');
        }
        for (const name of ['check', 'onState']) {
            if (options[name] !== undefined && typeof options[name] !== 'function') {
                throw new TypeError(name + ' must be a function');
            }
        }
        const supplied = options.timing === undefined ? {} : options.timing;
        if (!supplied || typeof supplied !== 'object' || Array.isArray(supplied) ||
            Object.keys(supplied).some(key => !Object.hasOwn(defaults, key))) {
            throw new TypeError('invalid connection recovery timing');
        }
        const timing = { ...defaults, ...supplied };
        for (const [name, value] of Object.entries(timing)) {
            const minimum = name === 'debounceMs' ? 0 : 1;
            const maximum = name === 'maxAttempts' ? 100 : 2147483647;
            if (!Number.isInteger(value) || value < minimum || value > maximum) {
                throw new RangeError('invalid connection recovery timing: ' + name);
            }
        }
        if (timing.retryMaxMs < timing.retryBaseMs || timing.suspendGapMs <= timing.watchIntervalMs) {
            throw new RangeError('invalid connection recovery timing relationships');
        }
        return { ...options, timing: Object.freeze(timing) };
    }

    function create(input) {
        const options = configuration(input);
        const timing = options.timing;
        const document = global.document;
        let closed = false;
        let pageHidden = false;
        let networkAvailable = global.navigator.onLine !== false;
        let dirty = document.hidden || !networkAvailable;
        let requested = false;
        let pendingReason = null;
        let epoch = 0;
        let operation = null;
        let debounce = null;
        let watcher = null;
        let lastTick = Date.now();
        let snapshot = Object.freeze({ state: 'idle', reason: null, attempt: 0, code: null });
        const listeners = [];

        function hidden() {
            return pageHidden || document.hidden || document.visibilityState === 'hidden';
        }
        function available() {
            return !hidden() && networkAvailable && global.navigator.onLine !== false;
        }
        function emit(state, reason, attempt = 0, code = null) {
            if (closed && state !== 'closed') return;
            if (snapshot.state === state && snapshot.reason === reason &&
                snapshot.attempt === attempt && snapshot.code === code) return;
            snapshot = Object.freeze({ state, reason, attempt, code });
            // Presentation failures must not alter connection recovery or create another log sink.
            try {
                Promise.resolve(options.onState?.(snapshot)).catch(() => {});
            } catch { /* The application owns observer diagnostics. */ }
        }
        function clearDebounce() {
            if (debounce !== null) global.clearTimeout(debounce);
            debounce = null;
        }
        function suspend(reason) {
            if (closed) return;
            // Every foreground return needs the application's channel check, even when
            // background timers kept running. Dirty requests verification, not a rebuild.
            dirty = true;
            requested = false;
            epoch += 1;
            clearDebounce();
            operation?.controller.abort(reason);
            emit('waiting', reason, operation?.attempt || 0);
        }
        function active(owner) {
            return !closed && operation === owner && owner.epoch === epoch &&
                !owner.controller.signal.aborted && available();
        }
        function guard(owner, controller, deadline) {
            if (!closed && !available()) suspend(hidden() ? 'hidden' : 'offline');
            if (Date.now() >= deadline) controller.abort(timeoutReason);
            if (!active(owner) || controller.signal.aborted) throw new Error('recovery cancelled');
        }
        function queue(reason) {
            if (closed) return;
            dirty = true;
            pendingReason = reason;
            if (!available()) {
                suspend(hidden() ? 'hidden' : 'offline');
                return;
            }
            // This opportunity accounts for any scheduling gap already observed.
            lastTick = Date.now();
            if (operation?.timedOut) operation.controller.abort('retry-requested');
            if (operation && !operation.controller.signal.aborted && !operation.finished) return;
            requested = true;
            if (operation || debounce !== null) return;
            debounce = global.setTimeout(() => {
                debounce = null;
                if (closed) return;
                if (!available()) { suspend(hidden() ? 'hidden' : 'offline'); return; }
                start();
            }, timing.debounceMs);
        }
        function delay(owner, milliseconds) {
            return new Promise(resolve => {
                const signal = owner.controller.signal;
                let timer = null;
                const done = () => {
                    if (timer !== null) global.clearTimeout(timer);
                    signal.removeEventListener('abort', done);
                    resolve();
                };
                signal.addEventListener('abort', done, { once: true });
                if (signal.aborted) done();
                else timer = global.setTimeout(done, milliseconds);
            });
        }
        async function attempt(owner) {
            owner.timedOut = false;
            const controller = new AbortController();
            const deadline = Date.now() + timing.attemptTimeoutMs;
            const signal = controller.signal;
            const context = Object.freeze({ signal, reason: owner.reason, attempt: owner.attempt });
            let phase = 'check';
            let abortListener;
            let timer = null;
            const forward = () => controller.abort(owner.controller.signal.reason);
            owner.controller.signal.addEventListener('abort', forward, { once: true });
            if (owner.controller.signal.aborted) forward();
            const aborted = new Promise(resolve => {
                abortListener = () => {
                    if (timer !== null) global.clearTimeout(timer);
                    resolve({ cancelled: true, code: signal.reason === timeoutReason ? 'timeout' : 'cancelled' });
                };
                signal.addEventListener('abort', abortListener, { once: true });
                if (signal.aborted) abortListener();
            });
            const work = Promise.resolve().then(async () => {
                guard(owner, controller, deadline);
                let healthy = false;
                if (options.check) {
                    emit('checking', owner.reason, owner.attempt);
                    guard(owner, controller, deadline);
                    try { healthy = await options.check(context); }
                    catch { guard(owner, controller, deadline); healthy = false; }
                    guard(owner, controller, deadline);
                    if (typeof healthy !== 'boolean') throw new TypeError('check must return a boolean');
                }
                if (!healthy) {
                    phase = 'reconnect';
                    emit('reconnecting', owner.reason, owner.attempt);
                    guard(owner, controller, deadline);
                    await options.reconnect(context);
                    guard(owner, controller, deadline);
                }
                phase = 'resync';
                emit('resyncing', owner.reason, owner.attempt);
                guard(owner, controller, deadline);
                await options.resync(context);
                guard(owner, controller, deadline);
            }).then(() => ({ ok: true }), () => ({ ok: false, code: phase + '_failed' }));
            if (!signal.aborted) timer = global.setTimeout(() => controller.abort(timeoutReason), timing.attemptTimeoutMs);
            try {
                const result = await Promise.race([work, aborted]);
                if (result.cancelled) {
                    if (result.code === 'timeout' && active(owner)) {
                        owner.timedOut = true;
                        dirty = true;
                        emit('failed', owner.reason, owner.attempt, 'timeout');
                    }
                    // Aborting cannot undo an uncooperative callback. Drain it before any retry.
                    await work;
                    return { ok: false, code: result.code };
                }
                return result;
            } finally {
                global.clearTimeout(timer);
                signal.removeEventListener('abort', abortListener);
                owner.controller.signal.removeEventListener('abort', forward);
            }
        }
        async function run(owner) {
            for (owner.attempt = 1; owner.attempt <= timing.maxAttempts; owner.attempt += 1) {
                if (!active(owner)) return;
                const result = await attempt(owner);
                if (!active(owner)) return;
                if (result.ok) {
                    dirty = false;
                    owner.finished = true;
                    emit('ready', owner.reason, owner.attempt);
                    return;
                }
                if (owner.attempt === timing.maxAttempts) {
                    dirty = true;
                    owner.finished = true;
                    emit('failed', owner.reason, owner.attempt, result.code);
                    return;
                }
                emit('retrying', owner.reason, owner.attempt, result.code);
                if (!active(owner)) return;
                await delay(owner, Math.min(timing.retryMaxMs, timing.retryBaseMs * 2 ** (owner.attempt - 1)));
            }
        }
        function start() {
            const owner = { controller: new AbortController(), epoch, reason: pendingReason || 'manual',
                attempt: 0, finished: false, timedOut: false };
            operation = owner;
            dirty = false;
            requested = false;
            pendingReason = null;
            void run(owner).catch(() => {
                if (active(owner)) {
                    dirty = true;
                    owner.finished = true;
                    emit('failed', owner.reason, owner.attempt, 'recovery_failed');
                }
            }).finally(() => {
                if (operation === owner) operation = null;
                if (!closed && requested && available()) queue(pendingReason || 'visible');
            });
        }
        function listen(target, name, callback) {
            target.addEventListener(name, callback);
            listeners.push(() => target.removeEventListener(name, callback));
        }
        function gap() { return Date.now() - lastTick >= timing.suspendGapMs; }
        function tick() {
            if (closed) return;
            const interrupted = gap();
            lastTick = Date.now();
            if (interrupted) {
                dirty = true;
                if (available()) queue('timer-gap');
            }
            if (!closed) watcher = global.setTimeout(tick, timing.watchIntervalMs);
        }
        listen(document, 'visibilitychange', () => {
            if (document.hidden || document.visibilityState === 'hidden') suspend('hidden');
            else {
                pageHidden = false;
                if (dirty || gap()) queue('visible');
            }
        });
        listen(global, 'pagehide', () => { pageHidden = true; suspend('hidden'); });
        listen(global, 'pageshow', event => {
            pageHidden = false;
            if (event.persisted || dirty || gap()) queue('pageshow');
        });
        listen(global, 'offline', () => { networkAvailable = false; suspend('offline'); });
        listen(global, 'online', () => {
            const wasAvailable = networkAvailable;
            networkAvailable = true;
            if (!wasAvailable || dirty || gap()) queue('online');
        });
        listen(global, 'focus', () => {
            if (!document.hidden && document.visibilityState !== 'hidden') pageHidden = false;
            if (dirty || gap()) queue('focus');
        });
        watcher = global.setTimeout(tick, timing.watchIntervalMs);

        function close() {
            if (closed) return;
            closed = true;
            epoch += 1;
            requested = false;
            clearDebounce();
            global.clearTimeout(watcher);
            watcher = null;
            for (const remove of listeners) remove();
            operation?.controller.abort('closed');
            emit('closed', 'closed');
        }
        return Object.freeze({ recover() { queue('manual'); }, close, get state() { return snapshot; } });
    }
    global.FnPackKitConnectionRecovery = Object.freeze({ create });
})(window);
