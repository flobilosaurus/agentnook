import { spawn } from "node:child_process";
import { lstatSync, readFileSync, readdirSync, unlinkSync } from "node:fs";
import { constants } from "node:os";
import { pathToFileURL } from "node:url";
import { createReporter } from "./report.mjs";

export function applyEvent(previous, data) {
  if (typeof data.sessionId !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(data.sessionId)) return previous;
  const next = { sessionId: data.sessionId, state: previous?.state ?? "idle" };
  switch (data.event) {
    case "SessionStart": next.state = "idle"; break;
    case "UserPromptSubmit":
    case "PostToolUse":
    case "PostToolUseFailure":
    case "PermissionDenied":
    case "ElicitationResult": next.state = "working"; break;
    case "PreToolUse": if (next.state !== "blocked") next.state = "working"; break;
    case "PermissionRequest":
    case "Elicitation": next.state = "blocked"; break;
    case "Stop":
    case "StopFailure": next.state = "idle"; break;
    case "SessionEnd": return undefined;
    case "Notification":
      if (["permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input"].includes(data.notification)) next.state = "blocked";
      else if (["idle_prompt", "agent_completed"].includes(data.notification)) next.state = "idle";
      else return previous;
      break;
    default: return previous;
  }
  return next;
}

export function supervise(args, { command = "claude", directory = "/run/agentnook-telemetry", interval = 250 } = {}) {
  const started = performance.now();
  const publish = createReporter({ file: `${directory}/status.json`, uptime: () => (performance.now() - started) / 1000 });
  const child = spawn(command, ["--settings", `${directory}/claude-settings.json`, ...args], { stdio: "inherit" });
  let state;
  const poll = () => {
    try {
      // Hooks use unique atomic files; sort by mtime to preserve their completion order.
      const events = readdirSync(`${directory}/events`).filter(name => /^[a-f0-9-]+\.json$/.test(name)).map(name => {
        const file = `${directory}/events/${name}`;
        const info = lstatSync(file);
        return { file, info };
      }).sort((a, b) => a.info.mtimeMs - b.info.mtimeMs);
      for (const { file, info } of events) {
        if (info.isFile() && info.size <= 4096) {
          try { state = applyEvent(state, JSON.parse(readFileSync(file, "utf8"))); } catch { /* Incomplete event. */ }
        }
        try { unlinkSync(file); } catch { /* Already removed. */ }
      }
    } catch { /* Reporter unavailable; agent keeps running. */ }
    if (state) publish(state.sessionId, state.state);
  };
  const timer = setInterval(poll, interval);
  const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
  // Terminal SIGINT reaches both foreground processes. Forwarding it again can
  // turn Claude's single-press cancellation into a double-press exit.
  const handlers = signals.map(signal => () => {
    if (signal === "SIGINT") return;
    try { child.kill(signal); } catch { /* Already exited. */ }
  });
  signals.forEach((signal, i) => process.on(signal, handlers[i]));
  const cleanup = () => {
    clearInterval(timer);
    signals.forEach((signal, i) => process.off(signal, handlers[i]));
  };
  child.on("error", () => { cleanup(); process.exitCode = 127; });
  child.on("exit", (code, signal) => { cleanup(); process.exitCode = code ?? 128 + (constants.signals[signal] ?? 0); });
  return child;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) supervise(process.argv.slice(2));
