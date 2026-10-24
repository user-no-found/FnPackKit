/** Opt-in external browser handoff for fnOS hosts, with visible clipboard recovery. */
export function createExternalLinks({ sdk, labels, timeoutMs = 5000 }) {
    const originalOpen = window.open;
    const nativeOpen = originalOpen.bind(window);
    let installed = false;
    let recovery = null;
    let closed = false;
    if (!Number.isInteger(timeoutMs) || timeoutMs < 250 || timeoutMs > 30000) {
        throw new Error('External navigation timeout must be 250..30000 milliseconds');
    }
    if (['title', 'copied', 'copyFailed', 'copy', 'open', 'close'].some(key => !labels?.[key])) {
        throw new Error('External navigation labels are required');
    }
    function destination(value) {
        const url = new URL(String(value), window.location.href);
        if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) {
            throw new Error('External navigation requires an HTTP(S) URL without credentials');
        }
        return url.href;
    }
    function bounded(operation) {
        let timer;
        return Promise.race([Promise.resolve(operation), new Promise((_, reject) => {
            timer = setTimeout(() => reject(new Error('Host unavailable')), timeoutMs);
        })]).finally(() => clearTimeout(timer));
    }
    async function copy(url) {
        try {
            await navigator.clipboard.writeText(url);
            return true;
        } catch { return false; }
    }
    function dismiss() {
        recovery?.remove();
        recovery = null;
    }
    function recover(url) {
        if (closed) return;
        dismiss();
        const panel = document.createElement('dialog');
        panel.setAttribute('aria-label', labels.title);
        panel.style.cssText = 'max-width:min(560px,90vw);padding:24px;border:1px solid #888;border-radius:12px;';
        const title = document.createElement('p');
        title.textContent = labels.title;
        const input = document.createElement('input');
        input.type = 'text'; input.readOnly = true; input.value = url;
        input.setAttribute('aria-label', labels.title);
        input.style.cssText = 'display:block;width:100%;box-sizing:border-box;margin-bottom:16px';
        input.addEventListener('click', () => input.select());
        const status = document.createElement('p');
        status.setAttribute('role', 'status');
        const button = (text, action) => {
            const node = document.createElement('button');
            node.textContent = text; node.type = 'button';
            node.style.marginInlineEnd = '8px';
            node.addEventListener('click', action); panel.append(node);
        };
        panel.append(title, input, status);
        const copyNow = async () => {
            const copied = await copy(url);
            status.textContent = copied ? labels.copied : labels.copyFailed;
            if (!copied) { input.focus(); input.select(); }
        };
        button(labels.copy, () => { void copyNow(); });
        button(labels.open, () => { open(url); });
        button(labels.close, dismiss);
        panel.addEventListener('close', dismiss, { once: true });
        document.body.append(panel); recovery = panel;
        panel.showModal();
        void copyNow();
    }
    function webOpen(url, target = '_blank', features = '') {
        let popup;
        try {
            const safeFeatures = features.split(',').filter(value =>
                !['noopener', 'noreferrer'].includes(value.trim().split('=')[0])).join(',');
            popup = nativeOpen('about:blank', target, safeFeatures);
            if (popup) { popup.opener = null; popup.location.replace(url); }
        } catch { popup?.close(); popup = null; }
        if (!popup) recover(url);
        return popup;
    }
    function mobileHandle() {
        let cancelled = false;
        let handedOff = false;
        const navigate = value => {
            const url = destination(value);
            if (cancelled || handedOff || closed) return;
            handedOff = true;
            void (async () => {
                try {
                    await bounded(sdk.ready());
                    if (cancelled || closed) return;
                    // A late host response must never open a second window through a fallback.
                    await bounded(sdk.openURL(url, '_blank'));
                } catch { if (!cancelled && !closed) recover(url); }
            })();
        };
        const location = { replace: navigate, assign: navigate };
        Object.defineProperty(location, 'href', { set: navigate });
        const handle = { opener: null, location, close() { cancelled = true; },
            get closed() { return cancelled; } };
        return { handle, navigate };
    }
    function open(value, target = '_blank', features = '') {
        const url = destination(value);
        if (!sdk || sdk.isWeb !== false) return webOpen(url, target, features);
        const pending = mobileHandle();
        pending.navigate(url);
        return pending.handle;
    }
    function wrappedOpen(value, target = '_blank', features = '') {
        if (!['_blank', ''].includes(target)) return nativeOpen(value, target, features);
        if (value === '' || value === undefined || value === 'about:blank') {
            return sdk?.isWeb === false ? mobileHandle().handle : nativeOpen(value, target, features);
        }
        let url;
        try { url = destination(value); } catch { return null; }
        if (new URL(url).origin === window.location.origin) return nativeOpen(value, target, features);
        return open(url, target, features);
    }
    function onClick(event) {
        if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
        const anchor = event.target.closest?.('a[href]');
        if (!anchor || anchor.hasAttribute('download') || anchor.target !== '_blank') return;
        let url;
        try { url = destination(anchor.href); } catch { return; }
        if (new URL(url).origin === window.location.origin) return;
        event.preventDefault(); open(url);
    }
    return {
        open,
        install() {
            if (installed) return;
            installed = true; window.open = wrappedOpen;
            document.addEventListener('click', onClick);
        },
        close() {
            closed = true;
            if (window.open === wrappedOpen) window.open = originalOpen;
            document.removeEventListener('click', onClick); dismiss();
        }
    };
}
