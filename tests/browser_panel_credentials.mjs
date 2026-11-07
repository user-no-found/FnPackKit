import assert from 'node:assert/strict';

const passwordLabel = '当前 NAS 账号的 sudo 密码';
const searchText = 'sun';
const composerText = 'sun 消息 fixture';

function password(page) { return page.getByLabel(passwordLabel, { exact: true }); }
function button(page, name) { return page.getByRole('button', { name, exact: true }); }

async function noPassword(page, reason) {
  assert.equal(await page.locator('input[type="password"]').count(), 0, reason);
}

async function idle(page) {
  await page.waitForFunction(() => {
    const shadow = document.querySelector('[data-fnpackkit-runtime]')?.shadowRoot;
    return [...(shadow?.querySelectorAll('button') || [])]
      .some(node => node.textContent === '刷新状态' && !node.disabled);
  });
}

async function removed(handle) {
  assert.deepEqual(await handle.evaluate(node => ({ value: node.value, connected: node.isConnected })),
    { value: '', connected: false }, 'old password input must be cleared and detached');
}

async function drafts(page) {
  assert.equal(await page.getByLabel('普通搜索', { exact: true }).inputValue(), searchText);
  assert.equal(await page.getByRole('textbox', { name: '消息编辑器' }).textContent(), composerText);
}

async function challenge(page, action = 'start') {
  if (action === 'update-backend') {
    await button(page, '更新后端').click();
    await noPassword(page, 'update confirmation must not mount password before POST');
    await button(page, '确认更新后端').click();
  } else await button(page, '启动后端').click();
  await password(page).waitFor({ state: 'visible' });
  await idle(page);
  assert.equal(await password(page).inputValue(), '');
  assert.equal(await page.locator('input[type="password"]').count(), 1);
  return password(page).elementHandle();
}

async function open(page) {
  await button(page, '后端控制').click();
  await button(page, '关闭控制面板').waitFor({ state: 'visible' });
  await idle(page);
}

async function initial(context, fixture, errors) {
  fixture.reset();
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    const nativeFrame = window.requestAnimationFrame.bind(window);
    window.fixtureFrames = { hold: false, pending: [] };
    window.requestAnimationFrame = callback => {
      if (!window.fixtureFrames.hold) return nativeFrame(callback);
      window.fixtureFrames.pending.push(callback);
      return window.fixtureFrames.pending.length;
    };
  });
  await page.goto(fixture.base + fixture.prefix + '/');
  await page.locator('[data-fnpackkit-runtime]').waitFor({ state: 'attached' });
  await page.waitForFunction(() =>
    document.querySelector('[data-fnpackkit-runtime]').shadowRoot.querySelector('.status')
      .textContent === '状态暂不可用');
  await noPassword(page, 'initial/status challenge must not create a password input');
  const search = page.getByLabel('普通搜索', { exact: true });
  await search.click();
  await search.pressSequentially(searchText);
  const composer = page.getByRole('textbox', { name: '消息编辑器' });
  await composer.fill(composerText);
  await drafts(page);
  return page;
}

async function ordinaryControls(page, fixture) {
  assert(fixture.queries > 0);
  assert.equal(fixture.posts.length, 0, 'initial status must be read-only');
  await open(page);
  await noPassword(page, 'opening controls must not create a password input');
  await button(page, '刷新状态').click();
  await idle(page);
  await noPassword(page, 'status refresh must not create a password input');
  await button(page, '更新后端').click();
  await button(page, '确认更新后端').waitFor({ state: 'visible' });
  await noPassword(page, 'ordinary destructive confirmation must not create a password input');
  assert.equal(fixture.posts.length, 0, 'confirmation alone must not send an operation');
  await button(page, '取消').click();
  await drafts(page);
}

async function teardownPaths(page, fixture) {
  for (const exit of ['取消', '关闭控制面板', 'Escape', 'switch']) {
    const handle = await challenge(page);
    await password(page).fill(fixture.secret);
    if (exit === 'Escape') await password(page).press('Escape');
    else if (exit === 'switch') await button(page, '更新后端').click();
    else await button(page, exit).click();
    await removed(handle);
    await noPassword(page, exit + ' must remove password input');
    assert.equal(fixture.operations, 0);
    if (exit === '关闭控制面板' || exit === 'Escape') {
      await page.getByLabel('普通搜索', { exact: true }).focus();
      await page.keyboard.press('End');
      await page.keyboard.type('!');
      await page.keyboard.press('Backspace');
      await open(page);
    }
    if (exit === 'switch') await button(page, '取消').click();
    await drafts(page);
  }
}

async function retryAndTerminalFailure(page, fixture) {
  const first = await challenge(page);
  const beforeEmpty = fixture.posts.length;
  await password(page).press('Enter');
  assert.equal(fixture.posts.length, beforeEmpty, 'empty authorization must not send a request');
  assert.equal(await password(page).inputValue(), '');
  await password(page).fill(fixture.wrongSecret);
  const retry = fixture.deferNext('challenge');
  await button(page, '授权并启动后端').click();
  await retry.received;
  await removed(first);
  await noPassword(page, 'password must be absent before POST response');
  assert.equal(fixture.posts.at(-1).password, fixture.wrongSecret);
  retry.release();
  await page.getByRole('alert').filter({ hasText: '授权未通过' }).waitFor();
  await password(page).waitFor({ state: 'visible' });
  await idle(page);
  assert.equal(await password(page).inputValue(), '');
  const next = await password(page).elementHandle();
  assert(await next.evaluate((node, previous) => node !== previous, first));
  await password(page).fill(fixture.secret);
  const terminal = fixture.deferNext('terminal');
  await button(page, '授权并启动后端').click();
  await terminal.received;
  await removed(next);
  await noPassword(page, 'terminal request must not retain a submitted password input');
  terminal.release();
  await page.getByRole('alert').filter({ hasText: '夹具拒绝此操作' }).waitFor();
  await idle(page);
  await noPassword(page, 'non-password failure must not recreate password input');
  assert.equal(fixture.operations, 0);
  await drafts(page);
}

async function lateChallenges(page, fixture) {
  for (const reopen of [false, true]) {
    const held = fixture.deferNext('challenge');
    await button(page, '启动后端').click();
    await held.received;
    await noPassword(page, 'request alone must not mount password input');
    await button(page, '关闭控制面板').click();
    if (reopen) {
      // Opening during a real in-flight request must not make that old intent current.
      await button(page, '后端控制').click();
      await button(page, '关闭控制面板').focus();
    } else await page.getByLabel('普通搜索', { exact: true }).focus();
    const focusBefore = await focusedLabel(page);
    held.release();
    await idle(page);
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
    await noPassword(page, 'late challenge after close/reopen must not resurrect input');
    assert.equal(await focusedLabel(page), focusBefore, 'late response must not steal focus');
    if (!reopen) await open(page);
    await drafts(page);
  }
}

async function focusedLabel(page) {
  return page.evaluate(() => {
    const active = document.activeElement?.shadowRoot?.activeElement || document.activeElement;
    return active?.getAttribute('aria-label') || active?.textContent;
  });
}

async function lateFrame(page, fixture) {
  await page.evaluate(() => { window.fixtureFrames.hold = true; });
  const old = await challenge(page);
  await password(page).fill(fixture.secret);
  assert(await page.evaluate(() => window.fixtureFrames.pending.length > 0));
  await button(page, '关闭控制面板').click();
  await removed(old);
  await page.getByLabel('普通搜索', { exact: true }).focus();
  await page.evaluate(() => {
    const frames = window.fixtureFrames;
    frames.hold = false;
    for (const callback of frames.pending.splice(0)) callback(performance.now());
  });
  await noPassword(page, 'late RAF must not recreate password input');
  assert.equal(await focusedLabel(page), '普通搜索', 'late RAF must not steal focus');
  await drafts(page);
  await open(page);
}

async function compactKeyboard(page, phone) {
  if (!phone) return;
  await page.waitForFunction(() =>
    document.querySelector('[data-fnpackkit-runtime]').dataset.compact === 'true');
  await button(page, '关闭控制面板').focus();
  let reached = false;
  for (let index = 0; index < 16; index++) {
    await page.keyboard.press('Tab');
    if (await focusedLabel(page) === passwordLabel) { reached = true; break; }
  }
  assert(reached, 'compact Tab navigation must reach password input, not buttons only');
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Tab');
  assert.equal(await focusedLabel(page), passwordLabel);
  // A reduced visual area models keyboard occlusion, not a real device keyboard.
  await page.setViewportSize({ width: 390, height: 420 });
  await password(page).scrollIntoViewIfNeeded();
  const bounds = await password(page).boundingBox();
  assert(bounds && bounds.y >= 0 && bounds.y + bounds.height <= 420);
}

async function authorizeOnce(page, fixture, phone) {
  const handle = await challenge(page, 'update-backend');
  await compactKeyboard(page, phone);
  await password(page).fill(fixture.secret);
  const held = fixture.deferNext('success');
  await password(page).press('Enter');
  await held.received;
  await removed(handle);
  await noPassword(page, 'Enter submission must clear/detach before fetch settles');
  assert.deepEqual(fixture.posts.at(-1), { action: 'update-backend', confirm: true, password: fixture.secret });
  assert.equal(fixture.posts.filter(post => post.action === 'update-backend' && post.password).length, 1);
  assert.equal(fixture.operations, 0);
  held.release();
  await page.getByRole('heading', { name: '已连接夹具', exact: true }).waitFor();
  await page.locator('[data-fnpackkit-runtime]').waitFor({ state: 'attached' });
  await noPassword(page, 'successful navigation must leave no password input');
  assert.equal(fixture.operations, 1, 'authorized operation must execute exactly once');
  await open(page);
  await button(page, '刷新状态').click();
  await idle(page);
  await noPassword(page, 'authorized status/open must not create password input');
  assert.equal(fixture.operations, 1, 'status must not start another operation');
  const stored = await page.evaluate(() => JSON.stringify([
    Object.entries(localStorage), Object.entries(sessionStorage), document.cookie,
  ]));
  assert(!stored.includes(fixture.secret) && !stored.includes(fixture.wrongSecret));
}

async function acceptedAfterClose(context, fixture, errors) {
  for (const reopen of [false, true]) {
    const page = await initial(context, fixture, errors);
    try {
      await open(page);
      const handle = await challenge(page);
      await password(page).fill(fixture.secret);
      const held = fixture.deferNext('success');
      await password(page).press('Enter');
      await held.received;
      await removed(handle);
      await button(page, '关闭控制面板').click();
      if (reopen) {
        await button(page, '后端控制').click();
        await button(page, '关闭控制面板').focus();
      } else await page.getByLabel('普通搜索', { exact: true }).focus();
      const focusBefore = await focusedLabel(page);
      held.release();
      await idle(page);
      await noPassword(page, 'late accepted operation must not restore authorization UI');
      assert.equal(fixture.operations, 1, 'closing UI must not cancel an accepted backend operation');
      assert.equal(await focusedLabel(page), focusBefore);
      assert.equal(await page.locator('h1').textContent(), '连接恢复夹具',
        'late success must not navigate away from the current application draft');
      await drafts(page);
    } finally { await page.close(); }
  }
}

export async function verifyPanelCredentials(context, fixture, phone, errors) {
  const page = await initial(context, fixture, errors);
  try {
    await ordinaryControls(page, fixture);
    await teardownPaths(page, fixture);
    await retryAndTerminalFailure(page, fixture);
    await lateChallenges(page, fixture);
    await lateFrame(page, fixture);
    await authorizeOnce(page, fixture, phone);
  } finally { await page.close(); }
  await acceptedAfterClose(context, fixture, errors);
  return {
    phone, initialStatusOpenConfirmAbsent: true, challengeOnly: true,
    clearedDetached: true, wrongPasswordFreshEmpty: true, terminalFailure: true,
    cancelCloseEscapeSwitch: true, lateChallengeCloseReopen: true,
    lateFrame: true, lateSuccessCloseReopen: true, ordinaryDraftsPreserved: true,
    compactKeyboard: phone, authorizedUpdateOnce: true, statusReadOnly: true,
    emptyAuthorizationReadOnly: true, noCredentialStorage: true,
  };
}
