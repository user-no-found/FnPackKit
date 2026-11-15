import assert from 'node:assert/strict';
import { controlButton, namedButton, stateIs, openControls, clickToggle } from './browser_panel_support.mjs';

export async function expireCredentialsUI(page) {
  await page.clock.pauseAt(new Date(Date.now() + 100));
  await page.clock.runFor(5000);
  await stateIs(page, 'half');
  await page.clock.resume();
}

export async function verifyIdleAuthorization(page, fixture, tools) {
  const { challenge, password, removed, noPassword, drafts, open, idle, focusedLabel } = tools;
  await page.clock.install();
  await page.clock.pauseAt(new Date(Date.now() + 100));
  try {
    const old = await challenge(page);
    await page.clock.runFor(4000);
    await password(page).fill(fixture.secret);
    await page.clock.runFor(4000);
    await password(page).pressSequentially('x');
    await page.clock.runFor(4999); await stateIs(page, 'open');
    assert.equal(await password(page).inputValue(), fixture.secret + 'x');
    await page.clock.runFor(1); await stateIs(page, 'half');
    await removed(old); await noPassword(page, 'idle must clear and detach an unsubmitted one-time password');
    assert.equal(fixture.operations, 0);
    await drafts(page);

    await open(page);
    const held = fixture.deferNext('challenge');
    await namedButton(page, '启动后端').click(); await held.received;
    const posts = fixture.posts.length;
    await page.clock.runFor(5000); await stateIs(page, 'half');
    await page.getByLabel('普通搜索', { exact: true }).focus();
    held.release(); await idle(page); await page.clock.runFor(32);
    await noPassword(page, 'idle during busy request invalidates late password challenges');
    assert.equal(fixture.posts.length, posts, 'idle must not replay a busy request');
    assert.equal(await focusedLabel(page), '普通搜索', 'late idle response must not steal draft focus');

    await open(page);
    await page.evaluate(() => { window.fixtureFrames.hold = true; });
    const frameInput = await challenge(page);
    await password(page).fill(fixture.secret);
    await page.clock.runFor(5000); await stateIs(page, 'half');
    await removed(frameInput);
    await page.getByLabel('普通搜索', { exact: true }).focus();
    await page.evaluate(() => {
      const frames = window.fixtureFrames; frames.hold = false;
      for (const callback of frames.pending.splice(0)) callback(performance.now());
    });
    await noPassword(page, 'idle-invalidated RAF must not resurrect a password');
    assert.equal(await focusedLabel(page), '普通搜索');
    await drafts(page);
    await open(page);
  } finally { await page.clock.resume(); }
}

export async function verifyDraftFocus(page, tools) {
  const { drafts, focusedLabel, phone } = tools;
  const close = async () => phone ? namedButton(page, '关闭控制面板').tap()
    : namedButton(page, '关闭控制面板').click();
  await page.evaluate(() => {
    window.fixtureEditorEscapeTrace = [];
    for (const type of ['keydown', 'keyup']) document.addEventListener(type, event => {
      if (event.key === 'Escape') window.fixtureEditorEscapeTrace.push(type);
    });
  });
  for (const editable of ['普通搜索', '消息编辑器']) {
    const draft = page.getByRole('textbox', { name: editable, exact: true });
    await draft.focus();
    if (await controlButton(page).getAttribute('aria-expanded') === 'true')
      await close();
    if (await page.locator('[data-fnpackkit-runtime]').getAttribute('data-ui-state') === 'half')
      await clickToggle(page, phone);
    await openControls(page, phone);
    assert.equal(await focusedLabel(page), editable, 'pointer controls preserve the real editable DOM focus');
    await page.evaluate(() => { window.fixtureEditorEscapeTrace = []; });
    await page.keyboard.press('Escape'); await stateIs(page, 'half');
    assert.equal(await focusedLabel(page), editable, 'editor-focused Escape closes without stealing draft focus');
    assert.deepEqual(await page.evaluate(() => window.fixtureEditorEscapeTrace), [],
      'the Escape pair owned by the controls must not leak to the business editor');
    await page.keyboard.press('Escape');
    assert.deepEqual(await page.evaluate(() => window.fixtureEditorEscapeTrace), ['keydown', 'keyup'],
      'a fresh Escape while controls are closed belongs to the business editor');
    await openControls(page, phone);
    await close(); await stateIs(page, 'half');
    assert.equal(await focusedLabel(page), editable, 'pointer close must not steal editable focus');
    await drafts(page);
  }
  await openControls(page, phone);
}
