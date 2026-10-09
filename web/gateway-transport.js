/* Acquire one same-origin HTTP proof for each WebSocket transport attempt. */
(function (global) {
    'use strict';
    const marker = 'fnpackkit.gateway.v1';
    const prefix = 'fpk-gateway.';

    function create(transportFactory, address, options) {
        if (typeof transportFactory !== 'function' ||
            typeof address !== 'string' || !options || typeof options.grantEndpoint !== 'string') {
            throw new Error('gateway transport requires a WebSocket factory and endpoints');
        }
        const target = new URL(address, global.location.href);
        const grant = new URL(options.grantEndpoint, global.location.href);
        const expectedScheme = global.location.protocol === 'https:' ? 'wss:' : 'ws:';
        if (target.protocol !== expectedScheme || target.host !== global.location.host || target.username ||
            target.password || target.hash || grant.origin !== global.location.origin ||
            grant.username || grant.password || grant.search || grant.hash || !options.grantEndpoint.trim()) {
            throw new Error('gateway transport endpoints must be same-origin');
        }
        const protocols = options.protocols === undefined ? [] :
            typeof options.protocols === 'string' ? [options.protocols] : options.protocols;
        if (!Array.isArray(protocols) || protocols.length > 32 || new Set(protocols).size !== protocols.length ||
            protocols.some(value => typeof value !== 'string' || !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(value) ||
                value === marker || value.startsWith(prefix))) {
            throw new Error('invalid application WebSocket protocols');
        }
        const timeout = options.timeoutMs === undefined ? 5000 : options.timeoutMs;
        if (!Number.isInteger(timeout) || timeout < 250 || timeout > 30000) {
            throw new Error('grant timeout must be 250..30000 milliseconds');
        }
        let inner = null;
        let controller = null;
        let generation = 0;
        let closed = false;
        let connected = false;
        async function start(sink) {
            if (closed) throw new Error('gateway transport is closed');
            if (controller || inner) throw new Error('gateway transport has already started');
            const attempt = ++generation;
            const active = new AbortController();
            controller = active;
            const timer = global.setTimeout(() => active.abort(), timeout);
            try {
                const response = await global.fetch(grant.href, {
                    method: 'POST', body: '', credentials: 'same-origin', cache: 'no-store',
                    redirect: 'error', signal: active.signal, headers: { 'Accept': 'application/json' }
                });
                if (!response.ok || !(response.headers.get('content-type') || '').startsWith('application/json')) {
                    throw new Error('handshake grant request rejected');
                }
                const reader = response.body.getReader();
                const chunks = [];
                let size = 0;
                while (true) {
                    const item = await reader.read();
                    if (item.done) break;
                    size += item.value.byteLength;
                    if (size > 4096) { await reader.cancel(); throw new Error('handshake grant response too large'); }
                    chunks.push(item.value);
                }
                const bytes = new Uint8Array(size);
                let offset = 0;
                for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
                const record = JSON.parse(new TextDecoder().decode(bytes));
                if (record.schema !== 1 || record.status !== 'granted' ||
                    typeof record.protocol !== 'string' || !/^fpk-gateway\.[A-Za-z0-9_-]{43}$/.test(record.protocol) ||
                    !Number.isInteger(record.expires_in_seconds) || record.expires_in_seconds < 1 || record.expires_in_seconds > 300) {
                    throw new Error('invalid handshake grant response');
                }
                if (closed || attempt !== generation) throw new Error('gateway transport attempt cancelled');
                inner = transportFactory(address, { protocols: [...protocols, marker, record.protocol] });
            } catch (error) {
                throw new Error('gateway handshake proof unavailable');
            } finally {
                global.clearTimeout(timer);
                if (controller === active) controller = null;
            }
            if (closed || attempt !== generation) { inner?.close(); throw new Error('gateway transport attempt cancelled'); }
            await inner.start({ ...sink, closed(error) {
                connected = false;
                sink.closed?.(error);
            } });
            if (closed || attempt !== generation) { inner?.close(); throw new Error('gateway transport attempt cancelled'); }
            connected = true;
        }
        function close() {
            closed = true;
            connected = false;
            generation += 1;
            controller?.abort();
            controller = null;
            inner?.close();
            inner = null;
        }
        return {
            name: 'websocket', start, close,
            get socket() { return inner?.socket; },
            send(data) {
                if (!connected || !inner) throw new Error('gateway transport is not connected');
                return inner.send(data);
            }
        };
    }
    global.FnPackKitGatewayTransport = Object.freeze({ create });
})(window);
