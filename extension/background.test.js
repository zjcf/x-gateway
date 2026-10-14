import test from 'node:test';
import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = (await readFile(new URL('./background.js', import.meta.url), 'utf8'))
  .replace("import {pageOperation} from './page.js';", 'const pageOperation = () => {};');
function event() {
  const listeners = new Set();
  return {addListener: fn => listeners.add(fn), removeListener: fn => listeners.delete(fn),
    fire: (...args) => {for (const listener of [...listeners]) listener(...args);}};
}
function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((done, fail) => {resolve = done; reject = fail;});
  return {promise, resolve, reject};
}
const tick = () => new Promise(resolve => setImmediate(resolve));
async function flush() {await tick(); await tick();}
async function setup(options = {}) {
  const state = {tabs: options.tabs || [{id: 12, url: 'https://example.test/login', status: 'complete'}],
    scripts: [], gates: new Map(), queryGate: undefined, calls: [], natives: [], alarms: [],
    requests: 0, writes: [], failures: options.failures || 0, now: Date.now(), timers: new Map(), nextTimer: 0};
  const chrome = {
    runtime: {id: 'extension-id', onMessage: event(), onInstalled: event(), onStartup: event(),
      connectNative(host) {
        assert.equal(host, 'com.x_gateway.accessctl');
        if (state.failures) {state.failures--; throw new Error('native unavailable');}
        const native = {onMessage: event(), onDisconnect: event(), sent: [],
          postMessage(message) {this.sent.push(structuredClone(message));},
          disconnect() {this.onDisconnect.fire();}};
        state.natives.push(native);
        return native;
      }},
    permissions: {onAdded: event(), onRemoved: event(),
      contains: async () => true, request() {state.requests++; throw new Error('must not request');}},
    alarms: {onAlarm: event(), create: async (name, options) => {state.alarms.push({name, ...options});}},
    storage: {local: {get: async () => options.freshProfile ? {} : {profile_id: 'profile'},
      set: async value => {state.writes.push(structuredClone(value));}}},
    tabs: {onCreated: event(), onUpdated: event(), onRemoved: event(),
      query: async () => {if (state.queryGate) await state.queryGate.promise; return structuredClone(state.tabs);},
      get: async id => {
        state.calls.push(id);
        if (state.gates.has(id)) await state.gates.get(id).promise;
        const tab = state.tabs.find(tab => tab.id === id);
        if (!tab) throw new Error('no tab');
        return {...tab};
      }},
    scripting: {executeScript: async options => {
      const tab = state.tabs.find(tab => tab.id === options.target.tabId);
      state.scripts.push({tabId: options.target.tabId, command: structuredClone(options.args[0])});
      return [{result: {ok: true, document: {url: tab.url, id: 'document'}}}];
    }}
  };
  vm.runInNewContext(source, {chrome, crypto: webcrypto, URL, structuredClone,
    Date: class extends Date {static now() {return state.now;}},
    setTimeout(fn, delay) {
      const id = ++state.nextTimer;
      state.timers.set(id, {fn, delay, at: state.now + delay});
      return id;
    },
    clearTimeout(id) {state.timers.delete(id);}
  });
  await flush();
  async function advance(ms) {
    const target = state.now + ms;
    for (let i = 0; i < 100; i++) {
      const next = [...state.timers].filter(([, timer]) => timer.at <= target).sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) {state.now = target; return;}
      state.now = next[1].at;
      state.timers.delete(next[0]);
      next[1].fn();
      await flush();
    }
    throw new Error('timer loop');
  }
  const message = value => new Promise(resolve => chrome.runtime.onMessage.fire(value, {id: chrome.runtime.id}, resolve));
  const command = (id, action, extra = {}) => ({type: 'command', id, tab_id: 12, action,
    deadline_ms: state.now + 5000, ...extra});
  const cancel = (native, id, cancelID, tabId = 12) => native.onMessage.fire({type: 'cancel', id, cancel_id: cancelID, tab_id: tabId});
  return {native: state.natives[0], state, chrome, advance, message, command, cancel};
}

test('automatically starts one profile bridge without popup or permission requests', async () => {
  const {native, state, chrome, message} = await setup({freshProfile: true});
  assert.equal(state.natives.length, 1);
  assert.deepEqual(native.sent[0].tabs, [{tab_id: 12, origin: 'https://example.test'}]);
  assert.equal(native.sent[0].type, 'hello');
  assert.equal(typeof native.sent[0].profile_id, 'string');
  assert.equal(typeof native.sent[0].bridge_id, 'string');
  assert.deepEqual(Object.keys(state.writes[0]), ['profile_id']);
  assert.deepEqual(state.alarms[0], {name: 'native-reconnect', periodInMinutes: 1});
  chrome.runtime.onInstalled.fire();
  chrome.runtime.onStartup.fire();
  await flush();
  assert.equal(state.natives.length, 1);
  assert.deepEqual(structuredClone(await message({type: 'status'})), {connected: true, permitted: true, tabs: 1});
  assert.equal(state.requests, 0);
});

test('hello inventory excludes privileged, hidden, invalid and incognito tabs and sends origins only', async () => {
  const {native} = await setup({tabs: [
    {id: 8, url: 'https://user:password@example.test/private?secret=yes#token', title: 'private title'},
    {id: 3, url: 'http://localhost:8765/a'},
    {id: 4, url: 'chrome://extensions/'}, {id: 5, url: 'file:///private/secret'},
    {id: 6, url: 'https://private.test/', incognito: true}, {id: 7}, {id: 0, url: 'https://invalid.test/'},
    {id: 9, url: 'invalid-url'}
  ]});
  assert.deepEqual(native.sent[0].tabs, [{tab_id: 3, origin: 'http://localhost:8765'}, {tab_id: 8, origin: 'https://example.test'}]);
  const serialized = JSON.stringify(native.sent);
  for (const secret of ['password', 'private title', 'secret=yes', 'token', 'private.test']) assert.equal(serialized.includes(secret), false);
});

test('inventory has a deterministic 1024 tab bound', async () => {
  const {native} = await setup({tabs: Array.from({length: 1030}, (_, i) => ({id: 1030 - i, url: 'https://example.test/a'}))});
  assert.equal(native.sent[0].tabs.length, 1024);
  assert.equal(native.sent[0].tabs[0].tab_id, 1);
  assert.equal(native.sent[0].tabs.at(-1).tab_id, 1024);
});

test('tab create, update and removal automatically synchronize after hello', async () => {
  const {native, state, chrome, advance, command} = await setup();
  state.tabs.push({id: 13, url: 'https://new.test/private?token=hidden'});
  chrome.tabs.onCreated.fire(state.tabs[1]);
  await advance(50);
  assert.deepEqual(native.sent.at(-1), {type: 'tabs', tabs: [
    {tab_id: 12, origin: 'https://example.test'}, {tab_id: 13, origin: 'https://new.test'}]});
  state.tabs[1].url = 'https://changed.test/hidden';
  chrome.tabs.onUpdated.fire(13, {url: state.tabs[1].url});
  await advance(50);
  assert.equal(native.sent.at(-1).tabs[1].origin, 'https://changed.test');
  state.tabs = state.tabs.filter(tab => tab.id !== 12);
  chrome.tabs.onRemoved.fire(12);
  const secret = command('removed', 'fill', {value: 'removed-secret'});
  native.onMessage.fire(secret);
  assert.equal(secret.value, undefined);
  await advance(50);
  assert.deepEqual(native.sent.at(-1), {type: 'tabs', tabs: [{tab_id: 13, origin: 'https://changed.test'}]});
  assert.equal(state.scripts.length, 0);
});

test('stale inventory query cannot publish on a replacement port', async () => {
  const {native, state, chrome, advance, message} = await setup();
  state.queryGate = deferred();
  chrome.tabs.onCreated.fire();
  await advance(50);
  const oldGate = state.queryGate;
  state.queryGate = undefined;
  await message({type: 'reconnect'});
  const replacement = state.natives[1];
  oldGate.resolve();
  await flush();
  await advance(50);
  assert.equal(native.sent.length, 1);
  assert.equal(replacement.sent[0].type, 'hello');
  assert.equal(replacement.sent.length, 1);
});

test('a stale failed inventory query cannot disconnect a replacement port', async () => {
  const {state, chrome, advance, message} = await setup();
  const oldGate = deferred();
  state.queryGate = oldGate;
  chrome.tabs.onCreated.fire();
  await advance(50);
  state.queryGate = undefined;
  await message({type: 'reconnect'});
  oldGate.reject(new Error('query failed'));
  await flush();
  assert.equal((await message({type: 'status'})).connected, true);
  assert.equal(state.natives.length, 2);
});

test('tab queues execute independently while preserving order within each tab', async () => {
  const {native, state, command} = await setup({tabs: [
    {id: 12, url: 'https://one.test/'}, {id: 13, url: 'https://two.test/'}]});
  state.gates.set(12, deferred());
  native.onMessage.fire(command('one', 'current'));
  native.onMessage.fire(command('one-next', 'visible'));
  native.onMessage.fire(command('two', 'current', {tab_id: 13}));
  await flush();
  assert.deepEqual(state.scripts.map(item => [item.tabId, item.command.id]), [[13, 'two']]);
  state.gates.get(12).resolve();
  await flush();
  assert.deepEqual(state.scripts.map(item => [item.tabId, item.command.id]), [[13, 'two'], [12, 'one'], [12, 'one-next']]);
});

test('unknown or missing tab_id and incognito commands are rejected and secrets released', async () => {
  const {native, state, command} = await setup({tabs: [
    {id: 12, url: 'https://one.test/'}, {id: 13, url: 'https://private.test/', incognito: true}]});
  for (const tabId of [undefined, 13, 999]) {
    const incoming = command('invalid-' + tabId, 'fill', {tab_id: tabId, value: 'invalid-secret'});
    native.onMessage.fire(incoming);
    assert.equal(incoming.value, undefined);
    assert.ok(native.sent.some(message => message.id === incoming.id && message.ok === false));
  }
  await flush();
  assert.equal(state.scripts.length, 0);
});

test('queued fill and click cancel immediately and release secrets', async () => {
  const {native, state, command, cancel} = await setup();
  state.gates.set(12, deferred());
  native.onMessage.fire(command('busy', 'current'));
  await flush();
  const fill = command('fill', 'fill', {value: 'canary-秘密', selector: '#password'});
  native.onMessage.fire(fill);
  native.onMessage.fire(command('click', 'click', {selector: '#submit'}));
  cancel(native, 'cancel-fill', 'fill');
  cancel(native, 'cancel-click', 'click');
  await flush();
  assert.equal(fill.value, undefined);
  assert.ok(native.sent.some(message => message.id === 'cancel-fill' && message.ok));
  assert.ok(native.sent.some(message => message.id === 'cancel-click' && message.ok));
  state.gates.get(12).resolve();
  await flush();
  assert.deepEqual(state.scripts.map(item => item.command.action), ['current']);
  assert.equal(JSON.stringify(native.sent).includes('canary-秘密'), false);
});

test('active cancellation during awaited API stops scripting and ack follows settling', async () => {
  const {native, state, command, cancel} = await setup();
  state.gates.set(12, deferred());
  const fill = command('fill', 'fill', {value: 'active-canary'});
  native.onMessage.fire(fill);
  await flush();
  cancel(native, 'cancel-fill', 'fill');
  await flush();
  assert.equal(fill.value, undefined);
  assert.equal(native.sent.some(message => message.id === 'cancel-fill'), false);
  state.gates.get(12).resolve();
  await flush();
  assert.equal(state.scripts.length, 0);
  assert.ok(native.sent.some(message => message.id === 'cancel-fill' && message.ok));
});

test('cancel cannot cross tab boundaries', async () => {
  const {native, state, command, cancel} = await setup({tabs: [
    {id: 12, url: 'https://one.test/'}, {id: 13, url: 'https://two.test/'}]});
  state.gates.set(12, deferred());
  const fill = command('fill', 'fill', {value: 'value'});
  native.onMessage.fire(fill);
  await flush();
  cancel(native, 'wrong-tab', 'fill', 13);
  assert.equal(fill.value, 'value');
  assert.ok(native.sent.some(message => message.id === 'wrong-tab' && !message.ok));
  cancel(native, 'right-tab', 'fill');
  state.gates.get(12).resolve();
  await flush();
  assert.equal(state.scripts.length, 0);
});

test('disconnect clears active and queued values; automatic reconnect never replays actions', async () => {
  const {native, state, advance, command} = await setup();
  state.gates.set(12, deferred());
  native.onMessage.fire(command('busy', 'current'));
  await flush();
  const fill = command('fill', 'fill', {value: 'disconnect-canary'});
  native.onMessage.fire(fill);
  native.disconnect();
  assert.equal(fill.value, undefined);
  state.gates.get(12).resolve();
  await flush();
  await advance(1000);
  const replacement = state.natives[1];
  assert.ok(replacement);
  assert.deepEqual(replacement.sent.map(message => message.type), ['hello']);
  const stale = command('stale', 'fill', {value: 'stale-canary'});
  native.onMessage.fire(stale);
  assert.equal(stale.value, undefined);
  await flush();
  assert.equal(state.scripts.length, 0);
  assert.equal(native.sent.length, 1);
  assert.notEqual(replacement.sent[0].bridge_id, native.sent[0].bridge_id);
});

test('failed native connections back off to 30 seconds and recover automatically', async () => {
  const {native, state, advance} = await setup();
  native.disconnect();
  state.failures = 7;
  const delays = [];
  for (let i = 0; i < 8; i++) {
    const timer = [...state.timers.values()].find(timer => timer.delay >= 1000);
    assert.ok(timer);
    delays.push(timer.delay);
    await advance(timer.at - state.now);
  }
  assert.deepEqual(delays, [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
  assert.equal(state.natives.length, 2);
  assert.equal(state.natives[1].sent[0].type, 'hello');
});

test('immediately rejected ports keep backoff; a stable connection resets it', async () => {
  const {native, state, advance} = await setup();
  native.disconnect();
  await advance(1000);
  state.natives.at(-1).disconnect();
  assert.ok([...state.timers.values()].some(timer => timer.delay === 2000));
  await advance(2000);
  await advance(30000);
  state.natives.at(-1).disconnect();
  assert.ok([...state.timers.values()].some(timer => timer.delay === 1000));
});

test('native host unavailable at startup recovers without popup', async () => {
  const {state, advance} = await setup({failures: 1});
  assert.equal(state.natives.length, 0);
  await advance(1000);
  assert.equal(state.natives.length, 1);
  assert.equal(state.natives[0].sent[0].type, 'hello');
});

test('alarm discovers missed tab events without opening a second bridge', async () => {
  const {native, state, chrome, advance} = await setup();
  state.tabs.push({id: 13, url: 'https://missed.test/private'});
  chrome.alarms.onAlarm.fire({name: 'native-reconnect'});
  await advance(50);
  assert.equal(state.natives.length, 1);
  assert.deepEqual(native.sent.at(-1).tabs, [
    {tab_id: 12, origin: 'https://example.test'}, {tab_id: 13, origin: 'https://missed.test'}]);
});

test('legacy and unknown actions are rejected without browser operations', async () => {
  const {native, state, command} = await setup();
  for (const action of ['navigate', 'clear', 'eval']) {
    const rejected = command(action, action, {url: 'https://example.test/next', value: 'unused-secret'});
    native.onMessage.fire(rejected);
    assert.equal(rejected.value, undefined);
  }
  await flush();
  assert.equal(state.tabs[0].url, 'https://example.test/login');
  assert.deepEqual(state.calls, []);
  assert.deepEqual(state.scripts, []);
  for (const id of ['navigate', 'clear', 'eval']) assert.ok(native.sent.some(message => message.id === id && !message.ok));
});

test('expired commands do not execute after an awaited API', async () => {
  const {native, state, command} = await setup();
  state.gates.set(12, deferred());
  native.onMessage.fire(command('expired', 'fill', {value: 'expired-secret'}));
  await flush();
  state.now += 6000;
  state.gates.get(12).resolve();
  await flush();
  assert.equal(state.scripts.length, 0);
  assert.ok(native.sent.some(message => message.id === 'expired' && !message.ok));
});
