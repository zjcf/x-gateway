import {pageOperation} from './page.js';

const host = 'com.x_gateway.accessctl';
const reconnectAlarm = 'native-reconnect';
const origins = ['http://*/*', 'https://*/*'];
const commands = new Map();
let port;
let connecting;
let reconnectTimer;
let retryDelay = 1000;
let refreshTimer;
let refreshing = false;
let refreshPending = false;

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (sender.id !== chrome.runtime.id) return;
  if (message.type === 'status') {
    status().then(respond, () => respond({connected: false, permitted: false}));
  } else if (message.type === 'reconnect') {
    reconnect().then(() => status()).then(respond, () => respond({connected: false}));
  } else return;
  return true;
});
chrome.runtime.onInstalled.addListener(initialize);
chrome.runtime.onStartup.addListener(initialize);
chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name !== reconnectAlarm) return;
  if (!reconnectTimer) void ensureConnected();
  scheduleRefresh();
});
chrome.tabs.onCreated.addListener(scheduleRefresh);
chrome.tabs.onUpdated.addListener(scheduleRefresh);
chrome.tabs.onRemoved.addListener(tabId => {
  // Reject commands for a closed tab even before the debounced inventory update.
  if (port) {
    port.tabs.delete(tabId);
    for (const state of commands.values()) if (state.tabId === tabId) cancelState(state);
  }
  scheduleRefresh();
});
chrome.permissions.onAdded.addListener(scheduleRefresh);
chrome.permissions.onRemoved.addListener(scheduleRefresh);
initialize();

function initialize() {
  // Timers are lost on service-worker shutdown; an alarm revives discovery.
  void Promise.resolve(chrome.alarms.create(reconnectAlarm, {periodInMinutes: 1})).catch(() => {});
  void ensureConnected();
}
async function status() {
  const permitted = await chrome.permissions.contains({origins});
  return {connected: Boolean(port?.ready), permitted, tabs: port?.tabs.size || 0};
}
async function reconnect() {
  await connecting;
  clearTimeout(reconnectTimer);
  reconnectTimer = undefined;
  retryDelay = 1000;
  if (port) dropPort(port);
  await ensureConnected();
}
function retry() {
  if (reconnectTimer || port) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = undefined;
    void ensureConnected();
  }, retryDelay);
  retryDelay = Math.min(retryDelay * 2, 30000);
}
function ensureConnected() {
  if (port || connecting) return connecting || Promise.resolve();
  connecting = connect().catch(() => {}).finally(() => {
    connecting = undefined;
    if (!port) retry();
  });
  return connecting;
}
async function inventory() {
  const tabs = await chrome.tabs.query({});
  const found = [];
  for (const tab of tabs) {
    if (!Number.isInteger(tab.id) || tab.id <= 0 || tab.incognito) continue;
    try {
      const url = new URL(tab.url);
      if (url.protocol === 'http:' || url.protocol === 'https:') found.push({tab_id: tab.id, origin: url.origin});
    } catch {}
  }
  return found.sort((a, b) => a.tab_id - b.tab_id).slice(0, 1024);
}
async function connect() {
  const stored = await chrome.storage.local.get('profile_id');
  const profileId = stored.profile_id || crypto.randomUUID();
  if (!stored.profile_id) await chrome.storage.local.set({profile_id: profileId});
  const tabs = await inventory();
  const native = chrome.runtime.connectNative(host);
  const currentPort = {native, tabs: new Map(tabs.map(tab => [tab.tab_id, tab.origin])),
    published: JSON.stringify(tabs), queues: new Map(), ready: false, connectedAt: Date.now()};
  port = currentPort;
  native.onMessage.addListener(command => receive(currentPort, command));
  native.onDisconnect.addListener(() => {
    // Access lastError in the event callback to acknowledge a missing native host.
    void chrome.runtime.lastError;
    if (port !== currentPort) return;
    if (Date.now() - currentPort.connectedAt >= 30000) retryDelay = 1000;
    dropPort(currentPort, false);
    retry();
  });
  try {
    native.postMessage({type: 'hello', profile_id: profileId, bridge_id: crypto.randomUUID(), tabs});
    if (port === currentPort) currentPort.ready = true;
  } catch {
    dropPort(currentPort);
    throw new Error('native connection failed');
  }
  scheduleRefresh();
}
function dropPort(currentPort, disconnect = true) {
  if (port === currentPort) port = undefined;
  currentPort.ready = false;
  for (const [id, state] of commands) if (state.port === currentPort) {cancelState(state); commands.delete(id);}
  currentPort.queues.clear();
  if (disconnect) {try {currentPort.native.disconnect();} catch {}}
}
function scheduleRefresh() {
  refreshPending = true;
  if (refreshTimer || refreshing) return;
  refreshTimer = setTimeout(() => {
    refreshTimer = undefined;
    void refreshInventory();
  }, 50);
}
async function refreshInventory() {
  if (refreshing) return;
  refreshing = true;
  try {
    while (refreshPending) {
      refreshPending = false;
      const currentPort = port;
      if (!currentPort?.ready) continue;
      const tabs = await inventory();
      if (port !== currentPort || !currentPort.ready) continue;
      const published = JSON.stringify(tabs);
      const next = new Map(tabs.map(tab => [tab.tab_id, tab.origin]));
      currentPort.tabs = next;
      for (const state of commands.values()) if (state.port === currentPort && !next.has(state.tabId)) cancelState(state);
      if (published !== currentPort.published) {
        try {currentPort.native.postMessage({type: 'tabs', tabs});}
        catch {if (port === currentPort) {dropPort(currentPort); retry();} return;}
        currentPort.published = published;
      }
    }
  } catch {} finally {refreshing = false;}
}
function cancelState(state) {
  state.cancelled = true;
  state.command.value = undefined;
  state.command.deadline_ms = 0;
}
function alive(state) {
  return !state.cancelled && port === state.port && state.port.ready && state.port.tabs.has(state.tabId) &&
    Number.isFinite(state.command.deadline_ms) && Date.now() < state.command.deadline_ms;
}
function reply(currentPort, id, result) {
  if (port !== currentPort || !currentPort.ready) return;
  // Never echo values, exception messages or DOM.
  try {currentPort.native.postMessage({type: 'result', id, ...result});} catch {dropPort(currentPort); retry();}
}
function receive(currentPort, command) {
  if (!command || typeof command !== 'object') return;
  if (port !== currentPort || !currentPort.ready || !Number.isInteger(command.tab_id) || !currentPort.tabs.has(command.tab_id)) {
    command.value = undefined;
    if (command.type === 'command' || command.type === 'cancel') reply(currentPort, command.id, {ok: false});
    return;
  }
  if (command.type === 'cancel') {
    // Cancellation bypasses every tab's queue and clears queued secret values immediately.
    const state = commands.get(command.cancel_id);
    if (state && (state.port !== currentPort || state.tabId !== command.tab_id)) {
      reply(currentPort, command.id, {ok: false});
      return;
    }
    if (state) cancelState(state);
    const settled = state?.active ? state.done : Promise.resolve();
    settled.then(() => reply(currentPort, command.id, {ok: true}));
    return;
  }
  if (command.type !== 'command' || commands.has(command.id)) {command.value = undefined; return;}
  if (!['current', 'visible', 'fill', 'click'].includes(command.action)) {
    command.value = undefined;
    reply(currentPort, command.id, {ok: false});
    return;
  }
  let done;
  const state = {command, port: currentPort, tabId: command.tab_id, cancelled: false, active: false,
    done: new Promise(resolve => {done = resolve;})};
  commands.set(command.id, state);
  const serial = (currentPort.queues.get(state.tabId) || Promise.resolve()).then(async () => {
    let result = {ok: false};
    try {
      if (alive(state)) {state.active = true; result = await execute(state);}
      if (!alive(state)) result = {ok: false};
    } catch {} finally {
      command.value = undefined;
      if (commands.get(command.id) === state) commands.delete(command.id);
      state.active = false;
      done();
    }
    reply(currentPort, command.id, result);
  });
  currentPort.queues.set(state.tabId, serial);
  void serial.finally(() => {if (currentPort.queues.get(state.tabId) === serial) currentPort.queues.delete(state.tabId);});
}

async function execute(state) {
  const command = state.command;
  if (!alive(state)) return {ok: false};
  const tab = await chrome.tabs.get(state.tabId);
  if (!alive(state) || tab.incognito) return {ok: false};
  const current = new URL(tab.url);
  if (!['http:', 'https:'].includes(current.protocol)) return {ok: false};
  const permitted = await chrome.permissions.contains({origins: [current.origin + '/*']});
  if (!alive(state) || !permitted) return {ok: false};
  const rows = await chrome.scripting.executeScript({
    target: {tabId: state.tabId, frameIds: [0]},
    func: pageOperation,
    args: [command]
  });
  if (!alive(state)) return {ok: false};
  const result = rows[0]?.result;
  if (!result || typeof result.ok !== 'boolean') return {ok: false};
  return result;
}
