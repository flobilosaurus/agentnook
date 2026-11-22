import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { once } from "node:events";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
const { createReporter } = await import(pathToFileURL(`${process.argv[2]}/report.mjs`).href);
const { applyEvent, supervise } = await import(pathToFileURL(`${process.argv[2]}/claude.mjs`).href);
const { recordEvent, runHook } = await import(pathToFileURL(`${process.argv[2]}/claude-hook.mjs`).href);

const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentnook-adapters-"));
const runId = "a".repeat(32);
process.env.AGENTNOOK_REPORT_ID = runId;
try {
  const file = path.join(root, "report.json");
  let uptime = 12;
  let publish = createReporter({ file, runId, uptime: () => uptime });
  publish("session-one", "working");
  const read = () => JSON.parse(fs.readFileSync(file, "utf8"));
  assert.equal(read().uptimeSeconds, 12);
  assert.equal(read().state, "working");
  assert.equal(fs.statSync(file).mode & 0o777, 0o600);
  const seq = read().seq;
  uptime = 42;
  publish = createReporter({ file, runId, uptime: () => uptime });
  publish("session-two", "idle");
  assert.ok(read().seq > seq, "reload preserves sequence progress");
  assert.equal(read().uptimeSeconds, 42);
  publish("../../bad", "idle");
  publish("session-two", "unknown");
  assert.equal(read().seq, seq + 1, "invalid states and IDs are ignored");
  createReporter({ file, runId: "bad" })("session-two", "working");
  assert.equal(read().seq, seq + 1);

  // Observe the real OpenCode v2 plugin with a typed-envelope-shaped event stream.
  const guestFile = "/run/agentnook-telemetry/status.json";
  const original = { read: fs.readFileSync, write: fs.writeFileSync, rename: fs.renameSync, set: globalThis.setInterval, clear: globalThis.clearInterval };
  const timers = new Set();
  const mapped = p => p === guestFile ? file : p === `${guestFile}.tmp` ? `${file}.tmp` : p;
  fs.readFileSync = (p, ...args) => original.read(mapped(p), ...args);
  fs.writeFileSync = (p, ...args) => original.write(mapped(p), ...args);
  fs.renameSync = (a, b) => original.rename(mapped(a), mapped(b));
  globalThis.setInterval = callback => { const timer = { callback, unref() {} }; timers.add(timer); return timer; };
  globalThis.clearInterval = timer => timers.delete(timer);
  syncBuiltinESMExports();
  try {
    const { default: plugin } = await import(pathToFileURL(`${process.argv[3]}/opencode/index.mjs`).href);
    let deliver, finish;
    const queue = [];
    let closed = false;
    const signalNext = () => { deliver?.(); deliver = undefined; };
    const ctx = {
      location: { directory: "/workspace" },
      event: { subscribe({ signal, types }) {
        assert.ok(types.includes("permission.asked"));
        signal.addEventListener("abort", () => { closed = true; signalNext(); });
        return (async function* () {
          try {
            while (!closed) {
              if (!queue.length) await new Promise(resolve => { deliver = resolve; });
              while (queue.length) yield queue.shift();
            }
          } finally { finish?.(); }
        })();
      } },
    };
    const cleanup = plugin.setup(ctx);
    const send = async (type, data, directory = "/workspace") => {
      queue.push({ type, data, location: { directory } }); signalNext();
      await new Promise(setImmediate);
    };
    assert.equal(read().state, "idle");
    await send("session.status", { sessionID: "one", status: { type: "busy" } });
    assert.equal(read().state, "working");
    await send("permission.asked", { sessionID: "one", id: "p1" });
    await send("permission.asked", { sessionID: "one", id: "p2" });
    await send("permission.replied", { sessionID: "one", requestID: "p1" });
    assert.equal(read().state, "blocked", "one remaining approval still blocks");
    await send("permission.replied", { sessionID: "one", requestID: "p2" });
    assert.equal(read().state, "working");
    await send("form.created", { form: { sessionID: "one", id: "f1" } });
    assert.equal(read().state, "blocked");
    await send("form.replied", { sessionID: "one", id: "f1" });
    assert.equal(read().state, "working");
    await send("session.status", { sessionID: "two", status: { type: "idle" } });
    assert.equal(read().state, "working", "another idle session does not hide active work");
    await send("session.status", { sessionID: "one", status: { type: "idle" } });
    assert.equal(read().state, "idle");
    await send("session.status", { sessionID: "foreign", status: { type: "busy" } }, "/other");
    assert.equal(read().state, "idle", "ignore unrelated locations");
    const beforeHeartbeat = read().seq;
    [...timers][0].callback();
    assert.ok(read().seq > beforeHeartbeat);
    const ended = new Promise(resolve => { finish = resolve; });
    cleanup(); await ended; await new Promise(setImmediate);
    assert.equal(timers.size, 0);
    delete process.env.AGENTNOOK_REPORT_ID;
    assert.equal(plugin.setup(ctx), undefined);
    process.env.AGENTNOOK_REPORT_ID = runId;
  } finally {
    fs.readFileSync = original.read; fs.writeFileSync = original.write; fs.renameSync = original.rename;
    globalThis.setInterval = original.set; globalThis.clearInterval = original.clear;
    syncBuiltinESMExports();
  }

  // Claude hooks never retain prompt/tool bodies or return approval decisions.
  const directory = path.join(root, "claude with spaces");
  fs.mkdirSync(path.join(directory, "events"), { recursive: true });
  recordEvent({ session_id: "main", hook_event_name: "PermissionRequest", prompt: "SECRET", tool_input: { token: "SECRET" } }, directory);
  const eventFiles = fs.readdirSync(path.join(directory, "events"));
  assert.equal(eventFiles.length, 1);
  const eventText = fs.readFileSync(path.join(directory, "events", eventFiles[0]), "utf8");
  assert.ok(!eventText.includes("SECRET"));
  recordEvent({ session_id: "main", hook_event_name: "Stop", agent_id: "subagent" }, directory);
  assert.equal(fs.readdirSync(path.join(directory, "events")).length, 1);
  await runHook((async function* () { yield "not JSON"; })());
  fs.unlinkSync(path.join(directory, "events", eventFiles[0]));

  let state;
  for (const [event, expected] of [["SessionStart", "idle"], ["UserPromptSubmit", "working"], ["PermissionRequest", "blocked"], ["PreToolUse", "blocked"], ["PostToolUse", "working"], ["Stop", "idle"]]) {
    state = applyEvent(state, { sessionId: "main", event }); assert.equal(state.state, expected);
  }
  assert.equal(applyEvent(state, { sessionId: "../../escape", event: "Stop" }), state);
  assert.equal(applyEvent(state, { sessionId: "main", event: "SessionEnd" }), undefined);

  // The supervisor preserves stdio/argv/exit code while maintaining heartbeats.
  const fake = path.join(directory, "fake-claude");
  fs.writeFileSync(fake, `#!${process.execPath}\nimport fs from 'node:fs';\nconst dir=${JSON.stringify(directory)};\nfs.writeFileSync(dir+'/argv.json',JSON.stringify(process.argv.slice(2)));\n${["SessionStart", "UserPromptSubmit", "PermissionRequest", "PostToolUse", "Stop"].map((event, i) => `setTimeout(()=>fs.writeFileSync(dir+'/events/${i}.json',JSON.stringify({sessionId:'main',event:${JSON.stringify(event)}})),${i * 80});`).join("\n")}\nsetTimeout(()=>process.exit(23),1500);\n`, { mode: 0o700 });
  // Numeric filenames are valid hexadecimal event names too.
  const seen = new Set();
  const statusFile = path.join(directory, "status.json");
  const poll = setInterval(() => { try { seen.add(JSON.parse(fs.readFileSync(statusFile)).state); } catch {} }, 10);
  const previousExitCode = process.exitCode;
  const child = supervise(["--continue", "prompt with spaces"], { command: fake, directory, interval: 10 });
  const [code] = await once(child, "exit");
  process.exitCode = previousExitCode;
  clearInterval(poll);
  assert.equal(code, 23);
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(directory, "argv.json"))), ["--settings", `${directory}/claude-settings.json`, "--continue", "prompt with spaces"]);
  assert.deepEqual([...seen].sort(), ["blocked", "idle", "working"]);
  const final = JSON.parse(fs.readFileSync(statusFile));
  assert.ok(final.seq > 5);
  assert.ok(final.uptimeSeconds >= 1, "uptime belongs to supervised agent, not hook processes");
  console.log("agentnook OpenCode/Claude adapters: OK");
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
