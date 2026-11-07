import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const source = await readFile(new URL('../runtime/runtimepanel/web/scope.js', import.meta.url), 'utf8');
const { scopeNotice, scopeConfirmation } = await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));

test('default and explicit user scope describe only the current account', () => {
  for (const scope of [undefined, '', 'user']) {
    assert.match(scopeNotice(scope), /当前账号/);
    assert.match(scopeConfirmation(scope, 'stop'), /当前账号/);
    assert.match(scopeConfirmation(scope, 'restart'), /^重启/);
  }
});

test('shared controls warn about everyone using the session and retaining data', () => {
  assert.match(scopeNotice('shared'), /所有正在使用/);
  assert.doesNotMatch(scopeNotice('shared'), /仅影响当前账号/);
  for (const action of ['stop', 'restart']) {
    const warning = scopeConfirmation('shared', action);
    assert.match(warning, /影响所有正在使用该会话的人/);
    assert.match(warning, /应用数据会保留/);
    assert.match(warning, /是否继续/);
  }
});
