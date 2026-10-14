import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./popup.js', import.meta.url), 'utf8');
const tick = () => new Promise(resolve => setImmediate(resolve));
async function setup(result = {connected: true, permitted: true, tabs: 2}) {
  const reconnect = {disabled: false, addEventListener(type, handler) {this.click = handler;}};
  const status = {textContent: ''};
  const permission = {textContent: ''};
  const state = {messages: [], requests: 0, result};
  const chrome = {
    permissions: {request() {state.requests++; throw new Error('must not request');}},
    runtime: {sendMessage: async message => {state.messages.push(structuredClone(message)); return state.result;}}
  };
  vm.runInNewContext(source, {chrome, document: {querySelector: selector =>
    selector === '#reconnect' ? reconnect : selector === '#status' ? status : permission}});
  await tick();
  return {reconnect, status, permission, state};
}
test('popup only displays automatic connection and permission state', async () => {
  const {status, permission, state} = await setup();
  assert.deepEqual(state.messages, [{type: 'status'}]);
  assert.equal(state.requests, 0);
  assert.match(status.textContent, /已自动连接.*2/);
  assert.match(permission.textContent, /所有 HTTP\/HTTPS/);
});
test('optional reconnect button does not request site permission or select a tab', async () => {
  const {reconnect, state} = await setup();
  await reconnect.click();
  assert.deepEqual(state.messages, [{type: 'status'}, {type: 'reconnect'}]);
  assert.equal(state.requests, 0);
  assert.equal(reconnect.disabled, false);
});
test('restricted permission status explains the Chrome setting without requesting access', async () => {
  const {status, permission, state} = await setup({connected: false, permitted: false});
  assert.match(status.textContent, /自动重连/);
  assert.match(status.textContent, /xweb Native Messaging/);
  assert.doesNotMatch(status.textContent, /daemon/);
  assert.match(permission.textContent, /扩展详情.*在所有网站上/);
  assert.equal(state.requests, 0);
});
