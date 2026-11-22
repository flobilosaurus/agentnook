import { readFileSync, renameSync, writeFileSync } from "node:fs";

// Guest-only display telemetry. No prompt, tool input or credentials are recorded.
export function createReporter({ file = "/run/agentnook-telemetry/status.json", runId = process.env.AGENTNOOK_REPORT_ID, uptime = () => process.uptime() } = {}) {
  if (!runId || !/^[a-f0-9]{32}$/.test(runId)) return () => {};
  let seq = 0;
  try {
    const previous = JSON.parse(readFileSync(file, "utf8"));
    if (previous.runId === runId && Number.isSafeInteger(previous.seq) && previous.seq > 0) seq = previous.seq;
  } catch { /* First report. */ }
  return (sessionId, state) => {
    if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(sessionId) || !["idle", "working", "blocked"].includes(state)) return;
    try {
      const report = { version: 2, runId, seq: ++seq, sessionId, state, uptimeSeconds: Math.max(0, Math.floor(uptime())) };
      writeFileSync(`${file}.tmp`, JSON.stringify(report), { mode: 0o600 });
      renameSync(`${file}.tmp`, file);
    } catch { /* Telemetry must never interrupt an agent. */ }
  };
}
