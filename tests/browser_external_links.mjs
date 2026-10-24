/** Real browser navigation and simulated fnOS mobile handoff/recovery boundaries. */
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';
import { pathToFileURL } from 'node:url';

const { launchBrowser, closeBrowser } = await import(pathToFileURL(process.env.BUILDFLOW_BROWSER_RUNTIME));
const source = await fs.readFile(new URL('../web/external-links.js', import.meta.url));
const checks = [];
const server = http.createServer((request, response) => {
    if (request.url === '/external-links.js') {
        response.writeHead(200, { 'Content-Type': 'text/javascript' }); response.end(source); return;
    }
    response.writeHead(200, { 'Content-Type': 'text/html' });
    if (request.url.startsWith('/destination')) { response.end('<title>Destination</title>Opened'); return; }
    response.end(`<!doctype html><meta name="referrer" content="no-referrer">
      <button id="open">Open</button><a id="anchor" target="_blank">Link</a>
      <script type="module">
      import {createExternalLinks} from '/external-links.js';
      const scenario = new URLSearchParams(location.search).get('scenario');
      window.calls = []; window.originalOpen = window.open;
      if (scenario === 'blocked' || scenario === 'denied') window.open = () => null;
      if (scenario === 'denied') Object.defineProperty(navigator, 'clipboard', {
        value: {writeText: () => Promise.reject(new Error('denied'))}, configurable: true});
      window.release = null;
      const gate = new Promise(resolve => { window.release = resolve; });
      const sdk = {isWeb: !['mobile', 'cancelled', 'timeout'].includes(scenario),
        ready: () => ['cancelled', 'timeout'].includes(scenario) ? gate : Promise.resolve(),
        openURL: async url => {window.calls.push(url)} };
      window.bridge = createExternalLinks({sdk, timeoutMs: 250, labels: {
        title: 'Open in browser', copied: 'Copied', copyFailed: 'Select and copy',
        copy: 'Copy', open: 'Open browser', close: 'Close'}});
      bridge.install();
      window.targetURL = location.origin.replace('127.0.0.1', 'localhost') + '/destination?state=private';
      document.querySelector('#open').onclick = () => {window.handle = window.open(targetURL, '_blank', 'noopener,noreferrer')};
      document.querySelector('#anchor').href = targetURL;
      window.fixtureReady = true;
      </script>`);
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
let browser;
try {
    browser = await launchBrowser();
    const context = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const load = async scenario => {
        await page.goto(`${origin}/?scenario=${scenario}`);
        await page.waitForFunction(() => window.fixtureReady);
    };
    for (const selector of ['#open', '#anchor']) {
        await load('web');
        const popupEvent = context.waitForEvent('page');
        await page.locator(selector).click();
        const popup = await popupEvent;
        await popup.waitForURL('**/destination?state=private');
        assert.equal(await popup.evaluate(() => window.opener), null);
        assert.equal(await page.getByRole('dialog').count(), 0);
        await popup.close();
    }
    checks.push('Web window.open and ordinary external anchors navigate with detached opener');
    await load('mobile');
    await page.locator('#open').click();
    await page.waitForFunction(() => calls.length === 1);
    assert.equal(await page.evaluate(() => calls[0]), await page.evaluate(() => targetURL));
    assert.equal(context.pages().length, 1);
    await page.evaluate(() => {
        window.pending = window.open('about:blank', '_blank');
        pending.opener = null; pending.location.replace(targetURL);
    });
    await page.waitForFunction(() => calls.length === 2);
    checks.push('Mobile SDK receives direct and deferred authorization URLs without blank browser tabs');
    await load('cancelled');
    await page.evaluate(() => {
        const pending = window.open('about:blank', '_blank');
        pending.location.replace(targetURL); pending.close(); release();
    });
    await page.waitForTimeout(300);
    assert.deepEqual(await page.evaluate(() => calls), []);
    checks.push('Cancelled deferred handoff never opens a browser after SDK readiness');
    await load('blocked');
    await page.locator('#open').click();
    await page.getByRole('status').filter({ hasText: 'Copied' }).waitFor();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), await page.evaluate(() => targetURL));
    await page.getByRole('button', { name: 'Close', exact: true }).click();
    checks.push('Blocked popup copies the exact link to the real browser clipboard and offers recovery');
    await load('denied');
    await page.locator('#open').click();
    await page.getByRole('status').filter({ hasText: 'Select and copy' }).waitFor();
    const input = page.getByRole('textbox');
    assert.equal(await input.inputValue(), await page.evaluate(() => targetURL));
    assert.equal(await input.evaluate(node => node.selectionEnd - node.selectionStart), (await input.inputValue()).length);
    checks.push('Clipboard denial leaves a visible selected URL for manual copying');
    await load('timeout');
    await page.locator('#open').click();
    await page.getByRole('dialog').waitFor();
    await page.evaluate(() => release());
    assert.deepEqual(await page.evaluate(() => calls), []);
    assert.equal(await page.evaluate(() => window.open('javascript:alert(1)', '_blank')), null);
    assert.equal(await page.evaluate(() => window.open('https://user:password@example.com/', '_blank')), null);
    checks.push('Host timeout offers recovery; unsafe schemes and URL credentials never reach the host');
    await page.evaluate(() => bridge.close());
    assert.equal(await page.evaluate(() => window.open === originalOpen), true);
    assert.deepEqual(errors, []);
    console.log(JSON.stringify({ checks, pageErrors: errors.length, realMobileHost: 'not exercised' }));
} finally {
    await closeBrowser(browser);
    await new Promise(resolve => server.close(resolve));
}
