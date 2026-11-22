import assert from 'node:assert/strict';
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { pathToFileURL } from 'node:url';

const runId = 'a'.repeat(32);
process.env.AGENTNOOK_REPORT_ID = runId;
const file = "/run/agentnook-telemetry/status.json";
const files = new Map();
const timers = new Set();
const original = {
  readFileSync: fs.readFileSync,
  writeFileSync: fs.writeFileSync,
  renameSync: fs.renameSync,
  setInterval: globalThis.setInterval,
  clearInterval: globalThis.clearInterval,
  uptime: process.uptime,
};
fs.readFileSync = (path, ...args) => {
  if (path !== file) return original.readFileSync(path, ...args);
  if (!files.has(path)) throw new Error('ENOENT');
  return files.get(path);
};
fs.writeFileSync = (path, value, options) => {
  assert.equal(path, `${file}.tmp`);
  assert.equal(options.mode, 0o600);
  files.set(path, value);
};
fs.renameSync = (source, target) => {
  assert.equal(source, `${file}.tmp`);
  assert.equal(target, file);
  files.set(target, files.get(source));
  files.delete(source);
};
globalThis.setInterval = (callback, ms) => {
  assert.equal(ms, 1000);
  const timer = { callback, unref() {} };
  timers.add(timer);
  return timer;
};
globalThis.clearInterval = (timer) => timers.delete(timer);
let uptime = 12;
process.uptime = () => uptime;
syncBuiltinESMExports();

try {
  const { default: extension } = await import(pathToFileURL(process.argv[2]).href);
  const setup = () => {
    const handlers = new Map();
    const events = new Map();
    extension({
      on: (event, handler) => handlers.set(event, handler),
      events: { on: (event, handler) => events.set(event, handler) },
    });
    return { handlers, events };
  };
  let { handlers, events } = setup();
  let idle = true;
  let sessionId = 'session-1';
  const ctx = { mode: 'tui', isIdle: () => idle, sessionManager: { getSessionId: () => sessionId } };
  const report = () => JSON.parse(files.get(file));

  handlers.get('session_start')({}, { ...ctx, mode: 'json' });
  assert.equal(files.size, 0);
  assert.equal(timers.size, 0);
  handlers.get('session_start')({}, ctx);
  assert.equal(report().state, 'idle');
  assert.equal(report().runId, runId);
  assert.equal(report().version, 2);
  assert.equal(report().uptimeSeconds, 12);
  assert.equal(timers.size, 1);
  const first = report().seq;
  uptime = 42;
  [...timers][0].callback();
  assert.equal(report().uptimeSeconds, 42);
  assert.ok(report().seq > first, 'unchanged state still advances heartbeat');

  idle = false;
  handlers.get('agent_start')({}, ctx);
  assert.equal(report().state, 'working');
  events.get('herdr:busy')({ active: true });
  idle = true;
  handlers.get('agent_settled')({}, ctx);
  assert.equal(report().state, 'working');
  events.get('herdr:blocked')({ active: true });
  events.get('herdr:blocked')({ active: true });
  assert.equal(report().state, 'blocked');
  events.get('herdr:blocked')({ active: false });
  assert.equal(report().state, 'blocked');
  events.get('herdr:blocked')({ active: false });
  assert.equal(report().state, 'working');
  events.get('herdr:busy')({ active: false });
  assert.equal(report().state, 'idle');

  const beforeInvalid = report().seq;
  for (const value of [null, true, {}, { active: 'true' }]) {
    events.get('herdr:busy')(value);
    events.get('herdr:blocked')(value);
  }
  assert.equal(report().seq, beforeInvalid, 'invalid events must not publish');

  const beforeReload = report().seq;
  handlers.get('session_shutdown')();
  assert.equal(timers.size, 0);
  ({ handlers, events } = setup());
  idle = false;
  sessionId = 'session-2';
  handlers.get('session_start')({ reason: 'reload' }, ctx);
  assert.equal(report().state, 'working');
  assert.equal(report().sessionId, 'session-2');
  assert.ok(report().seq > beforeReload);
  assert.equal(report().uptimeSeconds, 42, 'reload must not reset process uptime');
  events.get('herdr:busy')({ active: true });
  events.get('herdr:blocked')({ active: true });
  handlers.get('session_shutdown')();
  assert.equal(timers.size, 0);
  idle = true;
  handlers.get('session_start')({ reason: 'new' }, ctx);
  assert.equal(report().state, 'idle', 'session replacement resets busy/blocked state');
  assert.equal(report().uptimeSeconds, 42, 'session replacement must not reset process uptime');
  assert.equal(timers.size, 1, 'session replacement must not leak heartbeat timers');
  handlers.get('session_shutdown')();
  assert.equal(timers.size, 0);

  process.env.AGENTNOOK_REPORT_ID = '../../escape';
  assert.equal(setup().handlers.size, 0);
  delete process.env.AGENTNOOK_REPORT_ID;
  assert.equal(setup().handlers.size, 0);
  console.log('agentnook guest lifecycle: OK');
} finally {
  Object.assign(fs, {
    readFileSync: original.readFileSync,
    writeFileSync: original.writeFileSync,
    renameSync: original.renameSync,
  });
  process.uptime = original.uptime;
  globalThis.setInterval = original.setInterval;
  globalThis.clearInterval = original.clearInterval;
  syncBuiltinESMExports();
}
