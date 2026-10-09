/* Shared connection-state presentation; applications supply labels and transport events. */
(function (global) {
    'use strict';
    const reasons = new Set([
        'untrusted_peer', 'invalid_identity', 'invalid_host', 'invalid_path',
        'duplicate_origin', 'invalid_origin', 'invalid_fetch_metadata',
        'cross_site', 'origin_required', 'origin_mismatch', 'invalid_cookie',
        'runtime_unavailable', 'proxy_failure', 'grant_disabled', 'grant_invalid',
        'grant_capacity', 'grant_unavailable', 'unknown'
    ]);
    const requiredLabels = [
        'connecting', 'waiting', 'failed', 'checking', 'http_ready',
        'unavailable', 'rejected', 'request_id', 'http_status'
    ];

    function create(options) {
        if (!options || typeof options.endpoint !== 'string' || !options.endpoint.trim()) {
            throw new Error('diagnostic endpoint is required');
        }
        const endpoint = new URL(options.endpoint, global.location.href);
        if (endpoint.origin !== global.location.origin || endpoint.username ||
            endpoint.password || endpoint.search || endpoint.hash) {
            throw new Error('diagnostic endpoint must be a same-origin path');
        }
        const labels = options.labels;
        if (!labels || requiredLabels.some(key => typeof labels[key] !== 'string')) {
            throw new Error('connection status labels are required');
        }
        if (options.onRetry !== undefined &&
            (typeof options.onRetry !== 'function' || typeof labels.retry !== 'string')) {
            throw new Error('retry callback and label are required together');
        }
        const timeout = options.timeoutMs === undefined ? 5000 : options.timeoutMs;
        if (!Number.isInteger(timeout) || timeout < 250 || timeout > 30000) {
            throw new Error('diagnostic timeout must be 250..30000 milliseconds');
        }
        const container = options.container || document.body;
        const panel = document.createElement('div');
        panel.className = 'fnpackkit-connection-status';
        panel.setAttribute('role', 'status');
        panel.setAttribute('aria-live', 'polite');
        panel.hidden = true;
        const summary = document.createElement('div');
        const detail = document.createElement('div');
        detail.className = 'fnpackkit-connection-detail';
        panel.append(summary, detail);
        const retry = document.createElement('button');
        retry.type = 'button';
        retry.className = 'fnpackkit-connection-retry';
        retry.textContent = labels.retry || '';
        retry.hidden = true;
        if (options.onRetry) {
            retry.addEventListener('click', () => options.onRetry());
            panel.append(retry);
        }
        container.append(panel);
        let generation = 0;
        let probed = false;
        let controller = null;
        let closed = false;

        function show(state, text, extra = '') {
            if (closed) return;
            panel.hidden = false;
            panel.dataset.state = state;
            retry.hidden = state !== 'failed';
            summary.textContent = text;
            detail.textContent = extra;
        }
        function cancel() {
            generation += 1;
            if (controller) controller.abort();
            controller = null;
        }
        function connecting() {
            cancel();
            probed = false;
            show('connecting', labels.connecting);
        }
        function connected() {
            cancel();
            probed = false;
            show('waiting', labels.waiting);
        }
        function output() {
            cancel();
            probed = false;
            panel.hidden = true;
        }
        async function failed() {
            if (closed || probed) return;
            show('failed', labels.failed);
            probed = true;
            const attempt = generation;
            controller = new AbortController();
            const active = controller;
            const timer = global.setTimeout(() => active.abort(), timeout);
            let responseStatus = null;
            show('failed', labels.failed, labels.checking);
            try {
                const response = await global.fetch(endpoint.href, {
                    method: 'POST', credentials: 'same-origin', cache: 'no-store',
                    redirect: 'error', body: '', signal: active.signal,
                    headers: { 'Accept': 'application/json' }
                });
                responseStatus = response.status;
                if (!(response.headers.get('content-type') || '').toLowerCase().startsWith('application/json')) {
                    throw new Error('unexpected diagnostic response');
                }
                const reader = response.body.getReader();
                const chunks = [];
                let size = 0;
                while (true) {
                    const item = await reader.read();
                    if (item.done) break;
                    size += item.value.byteLength;
                    if (size > 4096) {
                        await reader.cancel();
                        throw new Error('diagnostic response too large');
                    }
                    chunks.push(item.value);
                }
                const bytes = new Uint8Array(size);
                let offset = 0;
                for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
                const record = JSON.parse(new TextDecoder().decode(bytes));
                if (closed || attempt !== generation) return;
                if (record.schema !== 1) throw new Error('unexpected diagnostic schema');
                if (response.ok && record.status === 'ready') {
                    show('failed', labels.failed, labels.http_ready);
                } else if ([403, 502, 503].includes(response.status) && record.status === 'rejected' && reasons.has(record.code)) {
                    let description = labels.reasons && labels.reasons[record.code];
                    if (typeof description !== 'string') description = labels.rejected;
                    const id = /^[a-f0-9]{32}$/.test(record.request_id) ? record.request_id : '';
                    show('failed', labels.failed, description + (id ? '\n' + labels.request_id + ' ' + id : ''));
                } else {
                    throw new Error('unexpected diagnostic status');
                }
            } catch (error) {
                if (!closed && attempt === generation) {
                    const description = responseStatus >= 400 ? labels.rejected : labels.unavailable;
                    const status = responseStatus === null ? '' : '\n' + labels.http_status + ' ' + responseStatus;
                    show('failed', labels.failed, description + status);
                }
            } finally {
                global.clearTimeout(timer);
                if (controller === active) controller = null;
            }
        }
        function close() {
            cancel();
            closed = true;
            panel.remove();
        }
        return { connecting, connected, output, failed, close };
    }
    global.FnPackKitConnectionStatus = Object.freeze({ create });
})(window);
