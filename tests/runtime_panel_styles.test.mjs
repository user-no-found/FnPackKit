import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const css = await readFile(new URL('../runtime/runtimepanel/web/panel.css', import.meta.url), 'utf8');
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([,selector,body]) => ({selector:selector.trim(),body}));

test('half clips its outside half at every edge, including interior toolbar and offset viewport bounds', () => {
  const clips = {left:'0 0 0 50%',right:'0 50% 0 0',top:'50% 0 0 0',bottom:'0 0 50% 0'};
  for (const [edge, clip] of Object.entries(clips)) {
    const rule = rules.find(item => item.selector === `.toggle[data-ui-state="half"][data-edge="${edge}"]`);
    assert.ok(rule, edge);
    assert.ok(rule.body.includes(`clip-path:inset(${clip})`), edge);
    assert.match(rule.body, /transform:translate[XY]\(-?50%\)/);
  }
  assert.ok(rules.filter(rule => /clip-path:/.test(rule.body)).every(rule => rule.selector.includes('[data-ui-state="half"]')),
    'revealed/open must retain the whole 44px touch control');
});

test('stationary hover/focus and coarse viewport cannot override the interaction state', () => {
  for (const rule of rules.filter(item => /:hover|:focus|data-compact/.test(item.selector))) {
    assert.doesNotMatch(rule.body, /\b(?:transform|clip-path|opacity|touch-action)\s*:/, rule.selector);
  }
  const toggle = rules.find(item => item.selector === '.toggle');
  assert.match(toggle.body, /width:44px/);
  assert.match(toggle.body, /height:44px/);
  assert.match(toggle.body, /touch-action:none/);
  assert.ok(!rules.some(rule => rule.selector === '.backdrop'));
});
