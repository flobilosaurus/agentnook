import { readFileSync, renameSync, writeFileSync } from "node:fs";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

// Embedded by agentnook and explicitly loaded only inside its guest.
export default function (pi: ExtensionAPI): void {
  const runId = process.env.AGENTNOOK_REPORT_ID;
  if (!runId || !/^[a-f0-9]{32}$/.test(runId)) return;
  const file = "/run/agentnook-telemetry/status.json";
  let sequence = 0;
  // /reload replaces this runtime, but the host must still see increasing heartbeats.
  try {
    const previous = JSON.parse(readFileSync(file, "utf8"));
    if (previous.runId === runId && Number.isSafeInteger(previous.seq) && previous.seq >= 0) sequence = previous.seq;
  } catch { /* First start has no report. */ }

  let context: ExtensionContext | undefined;
  let active = false;
  let busy = false;
  let blocked = 0;
  let heartbeat: ReturnType<typeof setInterval> | undefined;

  function publish(): void {
    if (!context) return;
    const report = {
      version: 2,
      runId,
      seq: ++sequence,
      uptimeSeconds: Math.floor(process.uptime()),
      sessionId: context.sessionManager.getSessionId(),
      state: blocked > 0 ? "blocked" : active || busy ? "working" : "idle",
    };
    try {
      writeFileSync(`${file}.tmp`, JSON.stringify(report), { mode: 0o600 });
      renameSync(`${file}.tmp`, file);
    } catch { /* Telemetry failure must not interrupt Pi or print guest data. */ }
  }

  pi.on("session_start", (_event, ctx) => {
    if (ctx.mode !== "tui") return;
    context = ctx;
    active = !ctx.isIdle();
    busy = false;
    blocked = 0;
    publish();
    if (heartbeat) clearInterval(heartbeat);
    heartbeat = setInterval(publish, 1000);
    heartbeat.unref();
  });
  pi.on("agent_start", (_event, ctx) => {
    if (!context) return;
    context = ctx;
    active = true;
    publish();
  });
  pi.on("agent_settled", (_event, ctx) => {
    if (!context || !ctx.isIdle()) return;
    active = false;
    publish();
  });
  pi.events.on("herdr:busy", (data: unknown) => {
    if (typeof data !== "object" || data === null || !("active" in data) || typeof data.active !== "boolean") return;
    busy = data.active;
    publish();
  });
  pi.events.on("herdr:blocked", (data: unknown) => {
    if (typeof data !== "object" || data === null || !("active" in data) || typeof data.active !== "boolean") return;
    blocked = data.active ? blocked + 1 : Math.max(0, blocked - 1);
    publish();
  });
  pi.on("session_shutdown", () => {
    if (heartbeat) clearInterval(heartbeat);
    heartbeat = undefined;
    context = undefined;
  });
}
