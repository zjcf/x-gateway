import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {pageOperation} from './page.js';
class Input {
  constructor() {this._value = ''; this.events = [];}
  get value() {return this._value;}
  set value(v) {this._value = v;}
  getClientRects() {return [1];}
  dispatchEvent(e) {this.events.push(e.type);}
  click() {this.clicked = true;}
}
globalThis.HTMLInputElement = Input;
globalThis.HTMLTextAreaElement = class extends Input {};
globalThis.getComputedStyle = () => ({visibility: 'visible'});
function setup() {
  const input = new Input();
  globalThis.window = {}; window.top = window;
  globalThis.location = {href: 'https://example.test/login'};
  globalThis.document = {querySelector: () => input};
  return input;
}
function op(command) {return pageOperation({deadline_ms: Date.now() + 10000, ...command});}
test('page operation remains self-contained when serialized for script injection', () => {
  const input = setup();
  const injected = vm.runInNewContext(`(${pageOperation.toString()})`,
    {window, location, document, HTMLInputElement, HTMLTextAreaElement, getComputedStyle, Event});
  const deadline_ms = Date.now() + 10000;
  const current = injected({action: 'current', deadline_ms}).document;
  assert.equal(injected({action: 'fill', selector: '#password', value: 'canary', document: current, deadline_ms}).ok, true);
  assert.equal(input.value, 'canary');
  assert.deepEqual(input.events, ['input', 'change']);
});
test('fill uses native setter and only the same top-frame document', () => {
  const input = setup(); const current = op({action: 'current'}).document;
  assert.equal(op({action: 'fill', selector: '#password', value: 'canary', document: current}).ok, true);
  assert.equal(input.value, 'canary'); assert.deepEqual(input.events, ['input', 'change']);
  location.href = 'https://evil.test/login';
  assert.equal(op({action: 'fill', selector: '#password', value: 'canary', document: current}).ok, false);
  assert.equal(input.value, 'canary');
});
test('legacy and unknown actions cannot modify fields or return values', () => {
  const input = setup();
  input.value = 'canary';
  for (const action of ['clear', 'navigate', 'eval']) {
    assert.deepEqual(op({action, selector: '#password', selectors: ['#password'], value: 'replacement'}), {ok: false});
  }
  assert.equal(input.value, 'canary');
  assert.deepEqual(input.events, []);
});
test('stale document, iframe and expired commands cannot fill or click', () => {
  const input = setup(); const current = op({action: 'current'}).document;
  assert.equal(op({action: 'fill', selector: '#p', value: 'secret', document: {...current, id: 'other'}}).ok, false);
  assert.equal(op({action: 'click', selector: '#submit', deadline_ms: Date.now() - 1}).ok, false);
  assert.equal(op({action: 'click', selector: '#submit', document: {...current, id: 'other'}}).ok, false);
  assert.equal(input.clicked, undefined);
  location.href = 'https://other.test/';
  assert.equal(op({action: 'click', selector: '#submit', document: current}).ok, false);
  assert.equal(input.clicked, undefined);
  window.top = {};
  assert.equal(op({action: 'fill', selector: '#p', value: 'secret', document: current}).ok, false);
});
test('arbitrary eval and invisible fields are rejected', () => {
  const input = setup();
  assert.equal(op({action: 'eval', value: 'alert(1)'}).ok, false);
  input.getClientRects = () => [];
  assert.equal(op({action: 'visible', selector: '#p'}).ok, false);
});
test('visibility hidden is not visible or clickable', () => {
  const input = setup();
  const doc = op({action: 'current'}).document;
  getComputedStyle = () => ({visibility: 'hidden'});
  assert.equal(op({action: 'visible', selector: '#p'}).ok, false);
  assert.equal(op({action: 'click', selector: '#p', document: doc}).ok, false);
  assert.equal(input.clicked, undefined);
  getComputedStyle = () => ({visibility: 'visible'});
});
